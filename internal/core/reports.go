package core

import (
        "encoding/base64"
        "fmt"
        "strings"
        "time"
)

// The official report systems (simplexmq [INFORMATION] section), verified
// against the simplexmq stable source and the two official RFCs in the
// simplex-chat repo (docs/rfcs/2024-12-28-reports.md and
// 2024-12-30-content-moderation.md):
//
//  1. In-chat reports (apps 6.3+): users report a message or FILE to the
//     group's owners and moderators. These reports are end-to-end
//     encrypted chat messages (the MCReport content type, reason spam /
//     illegal / community / other) that quote the reported message -
//     its name, size and arrival time for files. They never touch any
//     server, and the apps never expose the xftp:// link of a received
//     file, so a report identifies a file by WHEN it arrived and HOW BIG
//     it was - exactly what the file list's created-hour and size columns
//     show. Reports are also limited to groups: there is no report
//     command for one-to-one chats.
//
//  2. Complaints to the SERVER operator: the RFC describes reports to
//     third-party server operators as future work ("to the address sent
//     by the server during the initial connection") - today there is NO
//     in-app report button that contacts a third-party operator. What
//     exists today is the complaints contacts pair the server publishes
//     on its HTTPS info web page (Server/Web.hs serverInfoSubsts ->
//     complaintsSimplex / complaintsEmail fields):
//
//       [INFORMATION]
//       complaints_simplex = <your SimpleX address>
//       complaints_email = abuse@example.com               (optional)
//
//     A user who wants to report abuse opens the server's info page,
//     copies the address into their app ("connect via address") and
//     messages the operator - the reports arrive as ordinary chats.
//     The address should be your durable profile address (the app's
//     "SimpleX address"), NOT a one-time invitation link: one-time links
//     are a different connection mode and the server REFUSES them.
//
//     The whole section is only parsed when source_code is set (sxops
//     asserts that key at provisioning) and is read once at server
//     start, so changes need a restart. The NTF server ignores the
//     section entirely.
//
// THE CRASH (fixed in v1.6.19): the upstream INI parser
// (Server/Main.hs iniContacts) decodes complaints_simplex with
// `either error id` - ANY value its parser refuses is a fatal error and
// the server does not start (it "crashes" with e.g. "bad connection
// request mode"). v1.6.19 validates with the server's own grammar; the
// address the app shows for sharing has two spellings - the https form
// and the compact simplex:/ form the apps also share (v1.6.31 accepted
// it again after v1.6.28 had dropped it: the compact scheme parses
// upstream exactly like the https one):
//
//   full contact link:  https://<host>/contact#/?v=<range>&smp=smp://<keyHash>@<host>[:port]/<queueId>#<key>
//                       (the smp= value percent-escaped in the real links)
//                       simplex:/contact#/?v=..&smp=..          (compact)
//   short contact link: https://<host>[:port]/<a|c|g|r>[#]<key>[?h=..&p=..&c=..]
//                       simplex:/<a|c|g|r>#<key>?h=<host>[&p=..]  (compact -
//                       the server rides in the ?h= query, which the
//                       upstream parser REQUIRES on the compact form)
//
// (a = contact address, c = channel, g = group, r = relay - all are the
// CMContact connection mode the server requires; the query params mirror
// Agent/Protocol.hs connReqUriP / ConnShortLink strP exactly).

// complaintsKeys are the [INFORMATION] keys sxops manages. The full
// upstream section also has admin_simplex/admin_email (administrative
// contacts), operator/operator_country, website, hosting, hosting_type,
// server_country, usage_conditions and condition_amendments - sxops
// only drives the complaints pair; the rest can be hand-edited and is
// preserved by every patch.
const (
        complaintsSimplexKey = "complaints_simplex"
        complaintsEmailKey   = "complaints_email"
)

// ComplaintsSupported reports whether a kind publishes the complaints
// contacts (the SMP and XFTP servers parse [INFORMATION]; the NTF
// server does not).
func ComplaintsSupported(k Kind) bool {
        return k == KindSMP || k == KindXFTP
}

// Complaints reads the complaints contacts live from the server's INI:
// the SimpleX address users are offered for abuse reports ("" when
// unset - the info page then shows no contact) and the optional email.
// The INI is the source of truth, so hand-edits are picked up by the
// next refresh. Reading deliberately does NOT validate: when the INI
// holds a value the server refuses (e.g. left by a hand-edit), the
// caller can show it together with the warning from
// ValidateComplaintsSimplex so the operator can fix it here.
func (e *Engine) Complaints(k Kind) (simplex, email string, err error) {
        if !ComplaintsSupported(k) {
                return "", "", fmt.Errorf("%s does not publish complaints contacts", Def(k).Name)
        }
        if !e.State.Server(k).Installed {
                return "", "", fmt.Errorf("%s is not installed yet", Def(k).Name)
        }
        ini, err := e.loadServerIni(k)
        if err != nil {
                return "", "", err
        }
        s, _ := ini.Get("INFORMATION", complaintsSimplexKey)
        em, _ := ini.Get("INFORMATION", complaintsEmailKey)
        return strings.TrimSpace(s), strings.TrimSpace(em), nil
}

// SetComplaints writes the complaints contacts into the server's INI
// (empty values clear the key) and restarts the server when it is
// running, so the address appears on the info web page right away.
// Both values are validated before anything is written: the upstream
// parser (Server/Main.hs iniContacts -> either error) makes the server
// FAIL TO START on a complaints_simplex it cannot parse, so sxops never
// lets one reach the INI.
func (e *Engine) SetComplaints(k Kind, simplex, email string) error {
        if !ComplaintsSupported(k) {
                return fmt.Errorf("%s does not publish complaints contacts", Def(k).Name)
        }
        if !e.State.Server(k).Installed {
                return fmt.Errorf("%s is not installed yet", Def(k).Name)
        }
        simplex = strings.TrimSpace(simplex)
        email = strings.TrimSpace(email)
        if err := ValidateComplaintsSimplex(simplex); err != nil {
                return err
        }
        if err := ValidateComplaintsEmail(email); err != nil {
                return err
        }
        ini, err := e.loadServerIni(k)
        if err != nil {
                return err
        }
        if simplex != "" {
                ini.Set("INFORMATION", complaintsSimplexKey, simplex)
        } else {
                ini.Disable("INFORMATION", complaintsSimplexKey)
        }
        if email != "" {
                ini.Set("INFORMATION", complaintsEmailKey, email)
        } else {
                ini.Disable("INFORMATION", complaintsEmailKey)
        }
        if err := e.saveServerIni(k, ini); err != nil {
                return err
        }
        if !e.Backend.ServerRunning(k) {
                // not running: the INI applies at the next start
                return nil
        }
        // the servers read their INI once at start - restart to publish now
        if err := e.Backend.StopServer(k); err != nil {
                return fmt.Errorf("stopping %s after the INI change: %v", Def(k).Name, err)
        }
        if err := e.Backend.StartServer(k); err != nil {
                return fmt.Errorf("starting %s after the INI change: %v", Def(k).Name, err)
        }
        // wait briefly for the control port so an immediate UI refresh
        // does not race the server coming up (harmless if it never opens)
        e.waitControlPort(k, 10*time.Second)
        return nil
}

// ValidateComplaintsSimplex accepts exactly the address forms the
// servers parse in complaints_simplex (Agent/Protocol.hs: a full
// contact link via connReqUriP' or a short contact link via ConnShortLink
// strP - both must be the CMContact connection mode, and both come in an
// https spelling and a compact simplex:/ spelling, which v1.6.31 accepts
// again) or "" to clear the key. Everything else is rejected with the
// reason, BEFORE it can reach the INI and stop the server:
//
//   - one-time invitation links (https://host/invitation#/?... or
//     https://host/i#<id>/<key>, or their compact simplex:/invitation
//     and simplex:/i# spellings) are the CMInvitation mode -
//     the server rejects them with "bad connection request mode" and
//     would not start. Use your durable profile address instead.
//   - smp:// / xftp:// addresses are server addresses, not contact
//     addresses.
//   - full contact links must carry both the smp= and v= query params
//     (the server's parser requires both).
//   - compact short links must carry the ?h= server param (the upstream
//     parser refuses a compact short link without it - "short link
//     without server").
func ValidateComplaintsSimplex(s string) error {
        s = strings.TrimSpace(s)
        if s == "" {
                return nil
        }
        if strings.ContainsAny(s, " \t\r\n") {
                return fmt.Errorf("the address must be one whole token - copy it again without spaces or line breaks")
        }
        if len(s) > 2048 {
                return fmt.Errorf("this does not look like a SimpleX address (too long)")
        }
        if strings.HasPrefix(s, "smp://") || strings.HasPrefix(s, "xftp://") {
                return fmt.Errorf("that is a server address, not a contact address - paste the SimpleX address the app shows for your profile (it starts with https:// or simplex:/)")
        }
        if err := validateFullContactLink(s); err == nil {
                return nil
        }
        serr := validateShortContactLink(s)
        if serr == nil {
                return nil
        }
        // neither form parsed - say why in the most useful way
        if isInvitationLink(s) {
                return fmt.Errorf("that is a one-time invitation link - the server only accepts durable contact addresses (a wrong address stops it from starting). Copy your profile's SimpleX address in the app (profile -> SimpleX address, or Add contact -> Create address) and paste that instead")
        }
        // a compact spelling with a specific problem (e.g. the missing ?h=
        // server part) gets the validator's own error - it says exactly
        // what is wrong with the compact form
        low := strings.ToLower(s)
        if strings.HasPrefix(low, "simplex:/contact") || strings.HasPrefix(low, "simplex:/invitation") {
                if ferr := validateFullContactLink(s); ferr != nil && !strings.HasPrefix(ferr.Error(), "not a full") {
                        return ferr
                }
        }
        if strings.HasPrefix(low, "simplex:/") && !strings.HasPrefix(serr.Error(), "not a short") {
                return serr
        }
        return fmt.Errorf("this is not an address the server accepts (it would stop the server from starting): a full contact link https://host/contact#/?smp=...&v=..., a short reusable address like https://server/a#... - or the same links in their compact simplex:/ form - got %q", firstToken(s))
}

// ComplaintsSimplexForm describes the recognized address form (for the
// save result message): "full contact link", "contact address (short
// link)", "channel address (short link)", "group link (short link)",
// "relay link (short link)", or "" when the value is empty or invalid.
func ComplaintsSimplexForm(s string) string {
        s = strings.TrimSpace(s)
        if s == "" || ValidateComplaintsSimplex(s) != nil {
                return ""
        }
        if validateFullContactLink(s) == nil {
                return "full contact link"
        }
        switch shortLinkType(s) {
        case 'a':
                return "contact address (short link)"
        case 'c':
                return "channel address (short link)"
        case 'g':
                return "group link (short link)"
        case 'r':
                return "relay link (short link)"
        }
        return ""
}

// validateFullContactLink mirrors the upstream parser for the full
// contact link in both spellings: https://<host[:port]>/contact or the
// compact simplex:/contact, then the literal "#/?" <query>
// (Agent/Protocol.hs connReqUriP), mode exactly "contact" and a query
// that MUST carry the smp= (queue list) and v= (client version range)
// params. Returns nil when the value would parse upstream, an error
// otherwise.
func validateFullContactLink(s string) error {
        var query string
        switch {
        case strings.HasPrefix(s, "https://"):
                after := s[len("https://"):]
                slash := strings.IndexByte(after, '/')
                if slash <= 0 || !validAuthority(after[:slash]) || !strings.HasPrefix(after[slash:], "/contact") {
                        return fmt.Errorf("not a full contact link")
                }
                query = after[slash+len("/contact"):]
        case strings.HasPrefix(s, "simplex:/contact"):
                // the compact spelling (v1.6.31): same path, no authority -
                // the smp= queue URIs carry the servers
                query = s[len("simplex:/contact"):]
        default:
                return fmt.Errorf("not a full contact link")
        }
        // optional "/" then the literal "#/?" then the query (connReqUriP)
        if strings.HasPrefix(query, "/") {
                query = query[1:]
        }
        if !strings.HasPrefix(query, "#/?") {
                return fmt.Errorf("a full contact link has #/? after contact")
        }
        query = query[len("#/?"):]
        v := queryParamValue(query, "v")
        if v == "" {
                return fmt.Errorf("the full contact link is missing its v= (client version) part")
        }
        if !isVersionRange(v) {
                return fmt.Errorf("the v= part (%q) is not a client version range like 2 or 1-3", v)
        }
        smp := queryParamValue(query, "smp")
        if smp == "" {
                return fmt.Errorf("the full contact link is missing its smp= (server queue) part")
        }
        // every ";"-separated queue is a server URI, exactly as the app
        // encodes it (Protocol.hs strEncodeServer + SMPQueueUri strP):
        //
        //   smp://<keyHash>@<hosts>[:port]/<queueId>#<dhKey | /?v=..&dh=..>
        //
        // (a real link, percent-escaped as the query value:
        // smp=smp%3A%2F%2FPQUV...%3D%40smp6.simplex.im%2FK1rs...%23MCow...%3D)
        for _, q := range strings.Split(smp, ";") {
                if err := validateSmpQueueURI(q); err != nil {
                        return err
                }
        }
        return nil
}

// validateSmpQueueURI checks one queue of a full contact link's smp=
// value (after percent-decoding), mirroring the upstream parsers
// (Protocol.hs serverStrP / strEncodeServer, Agent/Protocol.hs
// SMPQueueUri strP):
//
//      smp://<keyHash>@<host>[,<host2>..][:port]/<queueId>#<dhKey>          (unversioned)
//      smp://<keyHash>@<host>[:port]/<queueId>[<dhKey>]/?v=<range>[&dh=..]  (versioned)
func validateSmpQueueURI(q string) error {
        rest, ok := strings.CutPrefix(q, "smp://")
        if !ok {
                return fmt.Errorf("the smp= part is not a server queue address (each queue starts with smp://)")
        }
        at := strings.IndexByte(rest, '@')
        if at <= 0 {
                return fmt.Errorf("the smp= queue is missing its @host part")
        }
        if !validBase64URLToken(rest[:at]) {
                return fmt.Errorf("the server key hash in the smp= part is not base64url")
        }
        tail := rest[at+1:]
        slash := strings.IndexByte(tail, '/')
        if slash <= 0 {
                return fmt.Errorf("the smp= queue is missing its /queueId part")
        }
        hosts := tail[:slash]
        if hosts == "" || strings.Contains(hosts, " ") {
                return fmt.Errorf("the smp= queue has no server host")
        }
        if segs := strings.Split(hosts, ","); segs[0] == "" {
                return fmt.Errorf("the smp= queue has an empty server host")
        }
        if colon := strings.LastIndexByte(hosts, ':'); colon >= 0 && !allDigits(hosts[colon+1:]) {
                return fmt.Errorf("the port in the smp= queue (%q) is not a number", hosts[colon+1:])
        }
        qrest := tail[slash+1:]
        hash := strings.IndexByte(qrest, '#')
        if hash <= 0 {
                return fmt.Errorf("the smp= queue is missing its #key part")
        }
        if !validBase64URLToken(qrest[:hash]) {
                return fmt.Errorf("the queue ID in the smp= part is not base64url")
        }
        after := qrest[hash+1:]
        if tq := strings.IndexByte(after, '?'); tq >= 0 {
                // versioned queue: [dhKey]"/"? then its own query with v=
                pre := strings.TrimSuffix(after[:tq], "/")
                if pre != "" && !validBase64URLToken(pre) {
                        return fmt.Errorf("the key in the smp= queue is not base64url")
                }
                if queryParamValue(after[tq+1:], "v") == "" {
                        return fmt.Errorf("the smp= queue is missing its v= part")
                }
        } else if !validBase64URLToken(after) {
                return fmt.Errorf("the key in the smp= queue is not base64url")
        }
        return nil
}

// validateShortContactLink mirrors the upstream parser for the short
// contact link in both spellings (Agent/Protocol.hs ConnShortLink
// strP): https://<host>[:port]/<a|c|g|r>[#]<key>[?h=..&p=..&c=..], or
// the compact simplex:/<a|c|g|r>#<key>?h=<server>[&p=..] - the compact
// form has no authority, so the upstream serverQueryP REQUIRES the ?h=
// server param ("short link without server" otherwise). The type
// letter selects the connection type (all are the
// CMContact mode the server requires; "i" is the one-time invitation
// type and is NOT accepted here). v1.6.31 re-admitted the compact
// spelling after v1.6.28 dropped it - the apps share it too.
func validateShortContactLink(s string) error {
        var body string
        compact := false
        switch {
        case strings.HasPrefix(s, "https://"):
                after := s[len("https://"):]
                slash := strings.IndexByte(after, '/')
                if slash <= 0 || !validAuthority(after[:slash]) {
                        return fmt.Errorf("not a short link")
                }
                body = after[slash+1:]
        case strings.HasPrefix(s, "simplex:/"):
                // the compact spelling: no host in the prefix - the
                // server must ride in the ?h= query param
                compact = true
                body = s[len("simplex:/"):]
        default:
                return fmt.Errorf("not a short link")
        }
        hash := strings.IndexByte(body, '#')
        if hash < 1 {
                return fmt.Errorf("a short address has # after its type letter")
        }
        typePart := body[:hash]
        if strings.HasSuffix(typePart, "/") {
                typePart = typePart[:len(typePart)-1]
        }
        if len(typePart) != 1 {
                return fmt.Errorf("not a short link")
        }
        switch typePart[0] {
        case 'a', 'A', 'c', 'C', 'g', 'G', 'r', 'R':
        default:
                return fmt.Errorf("%q is not a contact address type letter (a, c, g or r)", typePart)
        }
        rest := body[hash+1:]
        key, query := rest, ""
        if q := strings.IndexByte(rest, '?'); q >= 0 {
                key, query = rest[:q], rest[q+1:]
        }
        if !validBase64URLToken(key) {
                return fmt.Errorf("the part after # is not the address key (base64url)")
        }
        if p := queryParamValue(query, "p"); p != "" && !allDigits(p) {
                return fmt.Errorf("the port in this address (?p=%s) is not a number", p)
        }
        if compact {
                // the compact form's server: the ?h= param, required
                // upstream (AConnShortLink serverQueryP - h_ is Nothing
                // for the simplex: scheme, so a missing h= is a parse
                // failure). TransportHosts_ encodes multiple hosts
                // comma-separated; the first is the server.
                h := queryParamValue(query, "h")
                if i := strings.IndexByte(h, ','); i >= 0 {
                        h = h[:i]
                }
                if h == "" || strings.ContainsAny(h, " \t\"'<>#?@/") {
                        return fmt.Errorf("the compact simplex:/ address is missing its ?h= server part (the app includes it when it shares the address)")
                }
        }
        return nil
}

// shortLinkType returns the lowercase type letter of a valid short
// contact link ('a', 'c', 'g' or 'r') in either spelling, or 0 when s
// is not one.
func shortLinkType(s string) byte {
        var body string
        switch {
        case strings.HasPrefix(s, "https://"):
                after := s[len("https://"):]
                slash := strings.IndexByte(after, '/')
                if slash <= 0 {
                        return 0
                }
                body = after[slash+1:]
        case strings.HasPrefix(s, "simplex:/"):
                body = s[len("simplex:/"):]
        default:
                return 0
        }
        hash := strings.IndexByte(body, '#')
        if hash < 1 {
                return 0
        }
        typePart := body[:hash]
        if strings.HasSuffix(typePart, "/") {
                typePart = typePart[:len(typePart)-1]
        }
        if len(typePart) != 1 {
                return 0
        }
        c := typePart[0]
        if c >= 'A' && c <= 'Z' {
                c += 'a' - 'A'
        }
        switch c {
        case 'a', 'c', 'g', 'r':
                return c
        }
        return 0
}

// isInvitationLink reports whether s is a one-time invitation link in
// any form: the full https://host/invitation#/?... or the short
// https://host/i#<id>/<key>, or their compact simplex:/invitation and
// simplex:/i# spellings. These are the CMInvitation connection mode -
// the server's complaints_simplex parser refuses them.
func isInvitationLink(s string) bool {
        if rest, ok := strings.CutPrefix(s, "https://"); ok {
                if slash := strings.IndexByte(rest, '/'); slash > 0 {
                        tail := rest[slash+1:]
                        if strings.HasPrefix(tail, "invitation") || strings.HasPrefix(tail, "i#") || strings.HasPrefix(tail, "I#") {
                                return true
                        }
                }
        }
        if rest, ok := strings.CutPrefix(s, "simplex:/"); ok {
                if strings.HasPrefix(rest, "invitation") || strings.HasPrefix(rest, "i#") || strings.HasPrefix(rest, "I#") {
                        return true
                }
        }
        return false
}

// queryParamValue extracts a parameter's value from a URL query string
// ("k=v&k2=v2", the first match wins), percent-decoded like the
// upstream QSP parser decodes values.
func queryParamValue(query, name string) string {
        for _, pair := range strings.Split(query, "&") {
                if pair == "" {
                        continue
                }
                k, v, _ := strings.Cut(pair, "=")
                if k == name {
                        return percentDecode(v)
                }
        }
        return ""
}

// validAuthority checks the host[:port] part of an https:// address:
// non-empty, and when a colon is present the tail is a port number
// (bare IPv6 hosts in brackets are allowed).
func validAuthority(a string) bool {
        if a == "" || strings.ContainsAny(a, " \t\"'<>") {
                return false
        }
        if strings.HasPrefix(a, "[") { // IPv6 literal, maybe with :port
                end := strings.IndexByte(a, ']')
                return end > 1 && (len(a) == end+1 || (strings.HasPrefix(a[end+1:], ":") && allDigits(a[end+2:])))
        }
        if colon := strings.LastIndexByte(a, ':'); colon >= 0 {
                return allDigits(a[colon+1:])
        }
        return true
}

// validBase64URLToken reports whether s is a base64url token the
// upstream strP accepts for link keys and IDs (padded or unpadded) -
// checked by actually decoding it, like the Haskell parser does.
func validBase64URLToken(s string) bool {
        if s == "" {
                return false
        }
        enc := base64.RawURLEncoding
        if strings.Contains(s, "=") {
                enc = base64.URLEncoding
        }
        _, err := enc.DecodeString(s)
        return err == nil
}

// isVersionRange checks the VersionRange form the upstream parser
// accepts for v=: a single number ("2") or a min-max range ("1-3").
func isVersionRange(s string) bool {
        if s == "" {
                return false
        }
        for _, part := range strings.SplitN(s, "-", 2) {
                if !allDigits(part) {
                        return false
                }
        }
        return true
}

// ValidateComplaintsEmail accepts a plain address-like token or "".
func ValidateComplaintsEmail(s string) error {
        s = strings.TrimSpace(s)
        if s == "" {
                return nil
        }
        if strings.ContainsAny(s, " \t\r\n") {
                return fmt.Errorf("the email must be one whole token")
        }
        if !strings.Contains(s, "@") || strings.HasPrefix(s, "@") || strings.HasSuffix(s, "@") {
                return fmt.Errorf("this does not look like an email address")
        }
        return nil
}

// firstToken shortens a bad input for error messages.
func firstToken(s string) string {
        s = strings.TrimSpace(s)
        if i := strings.IndexAny(s, " \t\r\n"); i > 0 {
                return s[:i] + "…"
        }
        if len(s) > 24 {
                return s[:24] + "…"
        }
        return s
}
