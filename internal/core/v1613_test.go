package core

import (
        "encoding/base64"
        "encoding/hex"
        "strings"
        "testing"
)

// v1.6.13: reusable short links (SimpleX contact addresses, group /
// channel / relay invitation links) as moderation targets. The 6.4+ apps
// share compact links that carry only a link KEY - the link ID the
// servers resolve (SSenderLink fallback) is derived from it with
// HKDF-SHA512 "SimpleXContactLink" (simplexmq Crypto/ShortLink.hs
// contactShortLinkKdf), which sxops now derives identically, so banning
// by a pasted group or contact link works exactly like banning by an ID.

// TestDeriveContactLinkID pins the derivation to independently computed
// vectors (scripts/hkdf_vector.py, RFC 5869 with HMAC-SHA512): the first
// 24 bytes of HKDF(salt=empty, ikm=link key, info="SimpleXContactLink",
// L=56), base64url-encoded like every server entity ID.
func TestDeriveContactLinkID(t *testing.T) {
        cases := []struct {
                keyHex string
                want   string
        }{
                {"e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855", "fiiJAPUyVfD7G5GmZYavocyACx6M4PGC"},
                {"000102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f", "MjKttv1dDjCrM0lfbSRUNFjq46qysbU1"},
        }
        for _, c := range cases {
                raw, err := hex.DecodeString(c.keyHex)
                if err != nil {
                        t.Fatal(err)
                }
                token := base64.RawURLEncoding.EncodeToString(raw)
                got, ok := deriveContactLinkID(token)
                if !ok || got != c.want {
                        t.Errorf("deriveContactLinkID(%s) = %q, %v; want %q, true", token, got, ok, c.want)
                }
                // padded URL base64 of the same key must derive the same ID
                padded := base64.URLEncoding.EncodeToString(raw)
                if got2, ok2 := deriveContactLinkID(padded); !ok2 || got2 != c.want {
                        t.Errorf("padded token %s derived %q, %v; want %q", padded, got2, ok2, c.want)
                }
        }
        // keys that are not 32-byte base64url tokens derive nothing
        for _, bad := range []string{"", "tooshort", "!!!!not-base64!!!!", "AAECAwQFBgcICQoLDA0ODxAREhMUFRYXGBkaGxwdHh8="[:42] + "##"} {
                if got, ok := deriveContactLinkID(bad); ok || got != "" {
                        t.Errorf("deriveContactLinkID(%q) = %q, %v; want \"\", false", bad, got, ok)
                }
        }
}

// TestParseTargetIDShortLinks: the ban form accepts the short-link
// shapes the apps share - https://server/<a|c|g|r>#<key> (contact
// address / channel / group / relay), /i#<id>/<key> (one-time), and
// the simplex.im page-wrapped forms (?smp= / ?group=, percent-encoded,
// group pages double-encoded) - all resolving to the ID the servers
// ban by.
func TestParseTargetIDShortLinks(t *testing.T) {
        key1, _ := hex.DecodeString("e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855")
        key2, _ := hex.DecodeString("000102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f")
        tok1 := base64.RawURLEncoding.EncodeToString(key1)
        tok2 := base64.RawURLEncoding.EncodeToString(key2)
        id1 := "fiiJAPUyVfD7G5GmZYavocyACx6M4PGC"
        id2 := "MjKttv1dDjCrM0lfbSRUNFjq46qysbU1"
        const oneTimeID = "LNKSHORTTEST01AbCdEfGh"

        cases := []struct {
                name string
                in   string
                want string
        }{
                // v1.6.22: the accepted link forms are the https forms
                // and, since v1.6.31, the compact simplex:/ forms again
                // - the apps share both (the server-hosted short links,
                // the page-wrapped full links, and their compact
                // simplex:/ spellings).
                {"contact short link", "https://smp.example.com/a#" + tok1, id1},
                {"group short link", "https://smp.example.com/g#" + tok2, id2},
                {"channel short link", "https://smp.example.com/c#" + tok1, id1},
                {"relay short link", "https://smp.example.com/r#" + tok1, id1},
                {"group link uppercase letter", "https://smp.example.com/G#" + tok1, id1},
                {"older /A/# short link", "https://smp.example.com/A/#" + tok1, id1},
                {"one-time short link", "https://smp.example.com/i#" + oneTimeID + "/" + tok1, oneTimeID},
                {"one-time with query", "https://smp.example.com/i#" + oneTimeID + "/" + tok1 + "?h=host.example.com&p=5223", oneTimeID},
                {"server-hosted group link", "https://smp.example.com/g#" + tok1 + "?h=alt.example.com&p=5223&c=c29tZWhhc2g", id1},
                {"group link in report text", "abuse report: group invite https://smp.example.com/g#" + tok1 + " spamming", id1},
                {"page-wrapped full contact link", "https://simplex.im/contact#/?v=1&smp=smp%3A%2F%2F" + oneTimeID + "%3AkEy%40host.example.com%2F" + oneTimeID + "%23a2V5", oneTimeID},
                {"compact simplex form is a ban input again", "simplex:/g#" + tok2 + "?h=smp.example.com", id2},
                {"retired ?group= page form no longer parses", "https://simplex.im/contact#/?group=simplex%3A%2Fg%23" + tok2, ""},
                {"bad key length derives nothing", "https://smp.example.com/g#tooshort", ""},
                {"non-base64 key derives nothing", "https://smp.example.com/g#!!!!not-base64!!!!", ""},
                {"plain anchor is not a link", "https://example.com/page#section", ""},
                {"contact anchor with valid-looking type but bad key", "https://example.com/a#anchor", ""},
        }
        for _, c := range cases {
                if got := ParseTargetID(c.in); got != c.want {
                        t.Errorf("%s: ParseTargetID = %q, want %q", c.name, got, c.want)
                }
        }
}

// TestParseSMPStoreLogCreateLink: CREATE records carry link_id= when
// the queue was created WITH its connection link - and since v1.6.31
// only a CONTACT-mode queue's link_id is a live link (every reusable
// link kind rides QRContact -> queue_mode=C; a queue_mode=M queue's
// link_id is a one-time invitation leftover, an ordinary user's chat
// queue). LDELETE consumes a link, and a re-created queue without
// link_id clears it.
func TestParseSMPStoreLogCreateLink(t *testing.T) {
        log := "CREATE rid=r1 rk=k1 rdh=d1 sid=q1 sk=s2 queue_mode=M\n" + // plain messaging queue
                "CREATE rid=r1b rk=k1 rdh=d1 sid=q1b sk=s2 queue_mode=M link_id=lnk1bAAAAAAA queue_data=datablob\n" + // one-time invitation leftover: NOT a link
                "CREATE rid=r2 rk=k1 rdh=d1 sid=q2 sk=s2 queue_mode=C link_id=lnk2AAAAAAAA queue_data=datablob\n" + // created WITH its contact link
                "CREATE rid=r3 rk=k1 rdh=d1 sid=q3 sk=s2 queue_mode=C link_id=lnk3AAAAAAAA queue_data=datablob\n" +
                "LDELETE r3\n" + // link consumed / address deleted
                "CREATE rid=r4 rk=k1 rdh=d1 sid=q4 sk=s2 queue_mode=C link_id=lnk4AAAAAAAA queue_data=datablob status=blocked,reason=spam\n" + // banned, live link
                "CREATE rid=r5 rk=k1 rdh=d1 sid=q5 sk=s2 queue_mode=C link_id=lnk5AAAAAAAA queue_data=datablob\n" +
                "CREATE rid=r5 rk=k1 rdh=d1 sid=q5b sk=s2 queue_mode=C\n" // re-created without a link: clears
        rows := ParseStoreLog(KindSMP, []byte(log))
        byID := map[string]QueueInfo{}
        for _, r := range rows {
                byID[r.ID] = r
        }
        if len(rows) != 6 {
                t.Fatalf("rows = %d, want 6: %+v", len(rows), rows)
        }
        if q1 := byID["q1"]; q1.LinkID != "" || q1.LinkLive {
                t.Errorf("q1 (plain queue) should carry no link: %+v", q1)
        }
        if q1b := byID["q1b"]; q1b.LinkID != "" || q1b.LinkLive {
                t.Errorf("q1b (mode M with link_id - one-time invitation leftover) must not be a link: %+v", q1b)
        }
        if q2 := byID["q2"]; q2.LinkID != "lnk2AAAAAAAA" || !q2.LinkLive {
                t.Errorf("q2 should carry its live link from CREATE: %+v", q2)
        }
        if q3 := byID["q3"]; q3.LinkID != "lnk3AAAAAAAA" || q3.LinkLive {
                t.Errorf("q3's link was consumed (LDELETE): %+v", q3)
        }
        if q4 := byID["q4"]; !q4.Blocked || !q4.LinkLive {
                t.Errorf("q4 should be banned with a live link: %+v", q4)
        }
        if q5 := byID["q5b"]; q5.LinkID != "" || q5.LinkLive {
                t.Errorf("re-created queue must have its earlier link cleared: %+v", q5)
        }
}

// TestBanByShortLink: pasting a group link into the ban form bans the
// queue behind it - sxops derives the link ID exactly like the official
// clients do, the server resolves it to the queue via the SSenderLink
// fallback, and Unban works through the same pasted link.
func TestBanByShortLink(t *testing.T) {
        key, _ := hex.DecodeString("e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855")
        tok := base64.RawURLEncoding.EncodeToString(key)
        const derived = "fiiJAPUyVfD7G5GmZYavocyACx6M4PGC"
        const sid = "SIDGROUPLINK01"

        f := newFakeControlServer(t, true).addQueue(sid).addLink(derived, sid)
        rec := &ServerRec{ControlPort: f.port(), ControlAdmin: "tok"}

        pasted := "https://smp.example.com/g#" + tok
        target := ParseTargetID(pasted)
        if target != derived {
                t.Fatalf("group link parsed to %q, want the derived link ID %q", target, derived)
        }
        if _, err := BanClient(KindSMP, rec, target, BanReasonSpam, NewClientNotice(86400)); err != nil {
                t.Fatalf("ban by group link failed: %v", err)
        }
        if !f.blockedNow(sid) {
                t.Fatal("banning by the group link should block the queue behind it")
        }
        if !strings.Contains(f.wireLog(), "block "+derived+" reason=spam") {
                t.Fatalf("wire command should carry the derived link ID: %q", f.wireLog())
        }
        if _, err := UnbanClient(KindSMP, rec, ParseTargetID(pasted)); err != nil {
                t.Fatalf("unban by group link failed: %v", err)
        }
        if f.blockedNow(sid) {
                t.Fatal("unban by the group link should unblock the queue")
        }
}

// v1.6.13: the ban list is the server's own record now. The store log
// journals every control-port block with the full blocking info
// ("BLOCK <rId> reason=<r>[,notice={\"ttl\":...}]", written synchronously by
// logBlockQueue) and restored queues carry it as a status= token - so the
// admin panel derives the list (ID, reason, notice) from the journal and
// shows bans placed outside sxops too. UNBLOCK clears it; XFTP mirrors it
// with FBLK / the FNEW status token.

// TestParseSMPStoreLogBlockInfo pins the official blocking-info parsing
// of BLOCK records (ban + reason + notice TTL, unblock clearing, restored
// status= tokens).
func TestParseSMPStoreLogBlockInfo(t *testing.T) {
        log := "CREATE rid=r1 rk=k1 rdh=d1 sid=q1 sk=s2\n" +
                "BLOCK r1 reason=spam\n" + // banned, no notice
                "CREATE rid=r2 rk=k1 rdh=d1 sid=q2 sk=s2\n" +
                "BLOCK r2 reason=spam,notice={\"ttl\":null}\n" + // banned, no-expiry notice
                "CREATE rid=r3 rk=k1 rdh=d1 sid=q3 sk=s2\n" +
                "BLOCK r3 reason=content,notice={\"ttl\":86400}\n" + // banned, 1-day notice
                "UNBLOCK r3\n" + // unbanned: clears info
                "CREATE rid=r4 rk=k1 rdh=d1 sid=q4 sk=s2 status=blocked,reason=spam,notice={\"ttl\":2592000}\n" + // restored banned
                "CREATE rid=r5 rk=k1 rdh=d1 sid=q5 sk=s2\n" +
                "BLOCK r5 weird-token\n" + // malformed info: still banned, no info parsed
                "BLOCK r6 reason=spam\n" // unknown queue: ignored
        rows := ParseStoreLog(KindSMP, []byte(log))
        byID := map[string]QueueInfo{}
        for _, r := range rows {
                byID[r.ID] = r
        }
        if len(rows) != 5 {
                t.Fatalf("rows = %d, want 5: %+v", len(rows), rows)
        }
        q1 := byID["q1"]
        if !q1.Blocked || q1.BlockReason != "spam" || q1.BlockNotice {
                t.Errorf("q1 (reason only) misparsed: %+v", q1)
        }
        q2 := byID["q2"]
        if !q2.Blocked || q2.BlockReason != "spam" || !q2.BlockNotice || q2.BlockNoticeTTL != 0 {
                t.Errorf("q2 (null ttl notice) misparsed: %+v", q2)
        }
        q3 := byID["q3"]
        if q3.Blocked || q3.BlockReason != "" || q3.BlockNotice {
                t.Errorf("q3 should be unbanned with info cleared: %+v", q3)
        }
        q4 := byID["q4"]
        if !q4.Blocked || q4.BlockReason != "spam" || !q4.BlockNotice || q4.BlockNoticeTTL != 2592000 {
                t.Errorf("q4 (restored status= token) misparsed: %+v", q4)
        }
        q5 := byID["q5"]
        if !q5.Blocked || q5.BlockReason != "" || q5.BlockNotice {
                t.Errorf("q5 (malformed info) should be banned with no info: %+v", q5)
        }
}

// TestParseXFTPStoreLogBlockInfo pins the XFTP ban records: FBLK carries
// the official blocking info and the FNEW status token of restored files
// embeds it.
func TestParseXFTPStoreLogBlockInfo(t *testing.T) {
        log := "FNEW s1 key1 1000 dig1 1730000000 blocked,reason=spam,notice={\"ttl\":604800}\n" + // restored banned
                "FADD s1 rA:keyA\n" +
                "FNEW s2 key2 2000 dig2 1730003600\n" + // no status token (active)
                "FADD s2 rB:keyB\n" +
                "FBLK s2 reason=content,notice={\"ttl\":null}\n" + // banned via control port
                "FNEW s3 key3 3000 dig3 1730007200 off\n" + // retired: not a target
                "FADD s3 rC:keyC\n"
        rows := ParseStoreLog(KindXFTP, []byte(log))
        byID := map[string]QueueInfo{}
        for _, r := range rows {
                byID[r.ID] = r
        }
        if len(rows) != 2 {
                t.Fatalf("rows = %d, want 2: %+v", len(rows), rows)
        }
        a := byID["rA"]
        if !a.Blocked || a.BlockReason != "spam" || !a.BlockNotice || a.BlockNoticeTTL != 604800 {
                t.Errorf("rA (restored FNEW status) misparsed: %+v", a)
        }
        b := byID["rB"]
        if !b.Blocked || b.BlockReason != "content" || !b.BlockNotice || b.BlockNoticeTTL != 0 {
                t.Errorf("rB (FBLK record) misparsed: %+v", b)
        }
        if _, ok := byID["rC"]; ok {
                t.Errorf("retired file's recipient should not be listed: %+v", byID["rC"])
        }
}
