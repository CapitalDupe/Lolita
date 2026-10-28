package core

import (
        "crypto/hkdf"
        "crypto/sha512"
        "encoding/base64"
        "strings"
)

// TargetShape reports what kind of text a moderation target ID was
// parsed from - the ban dialog uses it to decide which options fit
// what is getting banned (v1.6.21; v1.6.22 narrowed the accepted paste
// forms; v1.6.31 re-admitted the compact simplex:/ scheme - the apps do
// share it and it parses upstream exactly like the https forms).
type TargetShape int

const (
        // ShapeBareID: a bare queue / link / file ID (or nothing parsed) -
        // which entity it is cannot be told from the text alone.
        ShapeBareID TargetShape = iota
        // ShapeLink: a connection link - a one-time invitation or a
        // reusable contact / group / channel / relay link in its https form
        // (the server-hosted short link or the page-wrapped full link).
        ShapeLink
)

// ParseTargetID extracts one moderation target ID from text pasted into
// the ban form (see ParseTargetIDShape for everything it accepts).
func ParseTargetID(raw string) string {
        id, _ := ParseTargetIDShape(raw)
        return id
}

// ParseTargetIDShape extracts one moderation target ID from text pasted
// into the ban form and also reports the shape of the text it came
// from, so the ban dialog can offer only the options that fit what is
// getting banned (v1.6.21). The official moderation commands (block /
// unblock / status / delete) accept a queue ID OR any connection link
// ID - and what a report actually carries is the link the reporter
// included, in one of its https forms (verified against the simplexmq
// v6.5.0 Agent/Protocol.hs grammar: connReqUriP for the full links,
// ConnShortLink for the short links):
//
//      https://<server>/a|c|g|r#<key>[?h=..]      short contact address /
//                                                 channel / group / relay
//                                                 link (the server form)
//      https://<server>/i#<linkId>/<key>[?h=..]   one-time invitation link
//      https://<server>/contact#/?v=..&smp=..     full contact / one-time
//      https://<server>/invitation#/?v=..&smp=..  link (page-wrapped; the
//                                                 simplex.im pages or the
//                                                 server's own host)
//      simplex:/a|c|g|r#<key>?h=<server>[&p=..]   the same short links in
//                                                 the compact in-app form
//      simplex:/i#<linkId>/<key>?h=<server>       (Agent/Protocol.hs
//                                                 AConnShortLink - the
//                                                 server rides in ?h=)
//      simplex:/contact#/?v=..&smp=..             the same full links in
//      simplex:/invitation#/?v=..&smp=..&e2e=..   the compact form
//                                                 (connReqUriP)
//      <bare queue, link or file ID>               raw ID
//
// v1.6.22 deliberately does NOT accept the bare smp:// / xftp://
// server addresses (smp:// is a server address - what the apps put in
// server settings, never a ban input). v1.6.28 briefly dropped the
// compact simplex:/ spellings too; v1.6.31 restored them (they are
// shared by the apps and parse upstream - see the grammar above).
// TargetInputHint explains the server-address paste.
//
// Reusable short links carry only a 32-byte link key - the moderation
// target is the link ID the official clients derive from it with
// HKDF-SHA512 ("SimpleXContactLink"), which sxops derives the same way
// (deriveContactLinkID). One-time short links carry the link ID itself.
// Full links percent-escape the smp= queue URI inside the query, so
// each line is percent-decoded until stable before matching; the
// moderation target of a full link is the PATH segment of its first
// smp:// queue URI - the queue (or address link) ID, never the key
// hash in front of the "@". "" when no ID-shaped token is found. A
// pasted link yields ShapeLink (a LINK ban); a bare token yields
// ShapeBareID (which entity it is cannot be told from the text).
func ParseTargetIDShape(raw string) (string, TargetShape) {
        for _, line := range strings.Split(raw, "\n") {
                line = strings.TrimSpace(line)
                if line == "" {
                        continue
                }
                // page-wrapped links percent-encode the queue URI in the query
                // string (sometimes twice) - decode until stable
                dec := percentDecodeStable(line)
                if id := parseFullLinkID(dec); id != "" {
                        return id, ShapeLink
                }
                if id := parseShortLinkID(dec); id != "" {
                        return id, ShapeLink
                }
                // a bare ID: the whole line must be one token
                if looksLikeID(dec) {
                        return dec, ShapeBareID
                }
        }
        return "", ShapeBareID
}

// TargetInputHint explains pasted text that yielded no moderation
// target (v1.6.22; v1.6.28 dropped the compact-form branch, v1.6.31
// mentions both link forms again): the known-wrong paste moderators
// reach for by mistake, or the generic prompt. Shown when the ban
// field holds text but nothing parsed.
func TargetInputHint(raw string) string {
        if strings.TrimSpace(raw) == "" {
                return "Paste what to ban: an invitation link (https:// or simplex:/ form)\nor a queue / link ID from the list below."
        }
        low := strings.ToLower(raw)
        if strings.Contains(low, "smp://") || strings.Contains(low, "xftp://") {
                return "That is a server address (smp:// / xftp://) - the form the apps put in\nserver settings, not something you ban. Paste the invitation link itself\n(https:// or simplex:/ form) or a queue / link ID from the list below."
        }
        return "No link or ID was found in that text. Paste the invitation link itself\n(https:// or simplex:/ form) or a queue / link ID from the list below."
}

// parseFullLinkID extracts the moderation target from a full connection
// link in either of its forms - the page-wrapped https form and the
// compact simplex:/ form - the links the apps share and the reports
// quote (Agent/Protocol.hs connReqUriP; v1.6.31 re-admitted the compact
// spelling, the scheme is the only difference):
//
//      https://<host>/contact#/ ?v=<range>&smp=<queueUri>[;..][&data=..]
//      https://<host>/invitation#/?v=..&smp=<queueUri>[;..]&e2e=..
//      simplex:/contact#/?v=..&smp=..            (the compact form)
//      simplex:/invitation#/?v=..&smp=..&e2e=..
//
// The smp= value percent-escapes the queue URI
// smp://<keyHash>@<host>[:port]/<queueId>#<dhKey> - the moderation
// target is its PATH segment (the queue ID, or the link ID of the
// modern address links). The part in front of the "@" is the server
// key hash, not the target - v1.6.21 mistakenly banned that. Links may
// sit inside pasted report prose, so every https:// and simplex:/
// token in the line is examined. "" when no full link is found.
func parseFullLinkID(s string) string {
        // v1.6.28: the scheme scan is case-insensitive - links copied out
        // of notes or PDFs can carry an upper-case HTTPS:// or SIMPLEX:/,
        // which used to defeat the scan and land the paste on the generic
        // "no link found" hint (the wrong ban variant). ToLower keeps the
        // byte offsets, so the original - with its case-sensitive IDs and
        // keys - is still what gets sliced.
        low := strings.ToLower(s)
        for rest, restLow := s, low; ; {
                // the next link token starts at whichever scheme comes
                // first (https:// or the compact simplex:/)
                i, n := nextLinkScheme(restLow)
                if i < 0 {
                        return ""
                }
                url := rest[i:]
                urlLow := restLow[i:]
                if end := strings.IndexAny(urlLow, " \t\"'<>"); end >= 0 {
                        url = url[:end]
                }
                for _, marker := range []string{"/contact#/?", "/contact/#/?", "/invitation#/?", "/invitation/#/?"} {
                        if j := strings.Index(urlLow, marker); j >= 0 {
                                if id := smpQueueURIPathID(queryParamValue(url[j+len(marker):], "smp")); id != "" {
                                        return id
                                }
                        }
                }
                // no full link in this URL - scan the rest of the line
                rest = rest[i+n:]
                restLow = restLow[i+n:]
        }
}

// nextLinkScheme finds the first link scheme in s (the lower-cased
// line) and returns its offset and length: https:// (8 bytes) or the
// compact simplex:/ (9 bytes). v1.6.31: the compact scheme is a link
// form again.
func nextLinkScheme(low string) (i, n int) {
        ih := strings.Index(low, "https://")
        ic := strings.Index(low, "simplex:/")
        switch {
        case ih < 0 && ic < 0:
                return -1, 0
        case ih < 0:
                return ic, len("simplex:/")
        case ic < 0 || ih < ic:
                return ih, len("https://")
        default:
                return ic, len("simplex:/")
        }
}

// smpQueueURIPathID extracts the entity ID from one smp:// queue URI -
// the path segment after the host: smp://<keyHash>@<host>[:port]/<id>
// #<dhKey>. Multi-queue values (";"-separated, the group links) take
// the first queue. "" when malformed.
func smpQueueURIPathID(uri string) string {
        uri = strings.TrimSpace(uri)
        if q := strings.IndexByte(uri, ';'); q >= 0 {
                uri = uri[:q]
        }
        if !strings.HasPrefix(uri, "smp://") {
                return ""
        }
        rest := uri[len("smp://"):]
        at := strings.IndexByte(rest, '@')
        if at < 0 {
                return ""
        }
        path := strings.IndexByte(rest[at:], '/')
        if path < 0 {
                return ""
        }
        id := rest[at+path+1:]
        if h := strings.IndexByte(id, '#'); h >= 0 {
                id = id[:h]
        }
        if !looksLikeID(id) {
                return ""
        }
        return id
}

// parseShortLinkID extracts the moderation target from a SimpleX short
// link in either of its forms - the https server form and the compact
// simplex:/ in-app form - the links the apps share and the reports
// quote (Agent/Protocol.hs ConnShortLink strP; v1.6.22 pinned the
// https forms, v1.6.31 re-admitted the compact spelling):
//
//      https://<server>/a#<key>?h=..         contact address (reusable)
//      https://<server>/c#<key>              channel link
//      https://<server>/g#<key>              group invitation link
//      https://<server>/r#<key>              relay link
//      https://<server>/i#<id>/<key>?h=..    one-time invitation link
//      simplex:/a#<key>?h=<server>[&p=..]   the same short links in the
//      simplex:/i#<id>/<key>?h=<server>     compact form - the server
//                                          rides in the ?h= query
//
// The type letter sits between "/" and "#" (an older variant allows a
// "/" between the letter and the "#"). For the one-time kind the link
// ID is in the URI; for the reusable kinds only the 32-byte link key
// is, and the link ID is derived from it exactly like the official
// clients do. Links may sit inside pasted report prose. "" when no
// short link is found.
func parseShortLinkID(s string) string {
        for i := 2; i < len(s); i++ {
                if s[i] != '#' {
                        continue
                }
                // the type letter just before "#", with an optional "/" between
                // them (the older /A/# links)
                j := i - 1
                if j >= 1 && s[j] == '/' {
                        j--
                }
                if j < 1 || s[j-1] != '/' || !shortLinkPrefixOK(s[:j-1]) {
                        continue
                }
                switch s[j] {
                case 'i', 'I':
                        // one-time link: the ID is the token before the "/"
                        // that separates it from the link key
                        if id := shortLinkToken(s[i+1:]); looksLikeID(id) {
                                return id
                        }
                case 'a', 'A', 'c', 'C', 'g', 'G', 'r', 'R':
                        // reusable link: derive the link ID from the key
                        if id, ok := deriveContactLinkID(shortLinkToken(s[i+1:])); ok {
                                return id
                        }
                }
        }
        return ""
}

// shortLinkPrefixOK checks the text before "/<type>#": an absolute URL
// ("https://server/g#...") whose host part is a plain host - no
// whitespace, no path/fragment/query characters and no "@" (so neither
// a short link nested inside some other URL nor the tail of a full
// link's embedded queue URI is mistaken for one) - or the compact
// scheme "simplex:" (v1.6.31) at a TOKEN boundary: either the whole
// prefix is exactly the scheme, or the scheme is preceded by a word
// separator (a compact link pasted inside report prose). A "simplex:/"
// that is merely the tail of some longer URL - e.g.
// "https://example.com/page/simplex:/g#..." - does not pass: its
// scheme is preceded by a path character, not a boundary. The compact
// short links carry no host in the prefix; the server rides in the ?h=
// query parameter (Agent/Protocol.hs AConnShortLink authorityP).
func shortLinkPrefixOK(prefix string) bool {
        if i := strings.LastIndex(strings.ToLower(prefix), "simplex:"); i >= 0 && i+len("simplex:") == len(prefix) {
                if i == 0 || strings.IndexAny(string(prefix[i-1]), " \t\"'(,<") >= 0 {
                        return true
                }
                return false
        }
        i := strings.LastIndex(prefix, "://")
        if i < 0 {
                return false
        }
        host := prefix[i+3:]
        return host != "" && !strings.ContainsAny(host, " \t\"'<>/#?@")
}

// shortLinkToken returns the base64url token that follows "#": it ends
// at the query ("?"), at the next component ("/" - the one-time link's
// key part), or at any delimiter.
func shortLinkToken(s string) string {
        end := len(s)
        for j := 0; j < len(s); j++ {
                switch s[j] {
                case '?', '/', '&', '"', '\'', '<', '>', ' ', '\t', ')':
                        end = j
                        return s[:end]
                }
        }
        return s[:end]
}

// deriveContactLinkID derives the moderation target of a reusable short
// link (contact address, channel, group or relay link) from its link
// key, the way the official clients do it (simplexmq
// Crypto/ShortLink.hs contactShortLinkKdf): the first 24 bytes of
// HKDF-SHA512 with the empty salt, the link key as the input key
// material and "SimpleXContactLink" as info - base64url-encoded like
// every server entity ID. ok is false when the token is not a 32-byte
// base64url key (all real link keys are 32 bytes).
func deriveContactLinkID(token string) (string, bool) {
        if token == "" {
                return "", false
        }
        enc := base64.RawURLEncoding
        if strings.Contains(token, "=") {
                enc = base64.URLEncoding
        }
        key, err := enc.DecodeString(token)
        if err != nil || len(key) != 32 {
                return "", false
        }
        okm, err := hkdf.Key(sha512.New, key, nil, "SimpleXContactLink", 56)
        if err != nil {
                return "", false
        }
        return base64.RawURLEncoding.EncodeToString(okm[:24]), true
}

// percentDecodeStable percent-decodes URL-escaped text until the result
// is stable: page-wrapped links escape the address once, group pages
// wrap an already-escaped address and so escape it twice.
func percentDecodeStable(s string) string {
        for n := 0; n < 4; n++ {
                d := percentDecode(s)
                if d == s {
                        return s
                }
                s = d
        }
        return s
}

// percentDecode replaces %XX escapes (both hex cases) with their bytes;
// malformed escapes are left as-is.
func percentDecode(s string) string {
        if !strings.ContainsRune(s, '%') {
                return s
        }
        var b strings.Builder
        b.Grow(len(s))
        for i := 0; i < len(s); {
                if s[i] == '%' && i+2 < len(s) && isHexDigit(s[i+1]) && isHexDigit(s[i+2]) {
                        b.WriteByte(hexDigitVal(s[i+1])<<4 | hexDigitVal(s[i+2]))
                        i += 3
                } else {
                        b.WriteByte(s[i])
                        i++
                }
        }
        return b.String()
}

func isHexDigit(c byte) bool {
        return c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F'
}

func hexDigitVal(c byte) byte {
        switch {
        case c >= '0' && c <= '9':
                return c - '0'
        case c >= 'a' && c <= 'f':
                return c - 'a' + 10
        default:
                return c - 'A' + 10
        }
}

// looksLikeID checks that a token could be a queue, link or file ID: the
// base64 alphabet (standard and URL-safe, with padding) and a plausible
// length.
func looksLikeID(s string) bool {
        if len(s) < 8 || len(s) > 128 {
                return false
        }
        for _, c := range s {
                switch {
                case c >= 'A' && c <= 'Z', c >= 'a' && c <= 'z', c >= '0' && c <= '9':
                case c == '+' || c == '/' || c == '=' || c == '-' || c == '_':
                default:
                        return false
                }
        }
        return true
}
