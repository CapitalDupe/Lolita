package core

import (
        "encoding/base64"
        "encoding/hex"
        "strings"
        "testing"
)

// v1.6.21 introduced ParseTargetIDShape; v1.6.22 pinned the accepted
// paste forms to what reports actually carry: the https link forms (the
// server-hosted short links and the page-wrapped full links) and bare
// IDs. v1.6.31 re-admitted the compact simplex:/ forms (the operator
// confirmed the apps share them; they parse upstream exactly like the
// https ones). The bare smp:// / xftp:// server addresses (what the
// apps put in server settings) stay rejected.

// TestParseTargetIDShape: the shape of each accepted paste.
func TestParseTargetIDShape(t *testing.T) {
        key, _ := hex.DecodeString("e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855")
        tok := base64.RawURLEncoding.EncodeToString(key)
        id1 := "fiiJAPUyVfD7G5GmZYavocyACx6M4PGC"
        const oneTimeID = "LNKSHAPECTEST01AbCdEfGh"
        const queueID = "K1rslx-m5bpXVIdMZg9NLUZ_8JBm8xTt"
        const keyHash = "PQUV2eL0t7OStZOoAsPEV2QYWt4-xilbakvGUGOItUo="
        // the real-world page-wrapped contact link form (v1.6.19 vectors):
        // the smp= query value percent-escapes the queue URI, the queue ID
        // is its PATH segment - never the key hash in front of the "@"
        escURI := "smp%3A%2F%2FPQUV2eL0t7OStZOoAsPEV2QYWt4-xilbakvGUGOItUo%3D%40smp6.simplex.im%2F" + queueID + "%23MCowBQYDK2VuAyEALDeVe-sG8mRY22LsXlPgiwTNs9dbiLrNuA7f3ZMAJ2w%3D"
        rawURI := "smp://" + keyHash + "@smp6.simplex.im/" + queueID + "#MCowBQYDK2VuAyEALDeVe-sG8mRY22LsXlPgiwTNs9dbiLrNuA7f3ZMAJ2w="

        cases := []struct {
                name      string
                in        string
                wantID    string
                wantShape TargetShape
        }{
                // https short links (the server form the apps share)
                {"contact short link", "https://smp.example.com/a#" + tok, id1, ShapeLink},
                {"group short link", "https://smp.example.com/g#" + tok, id1, ShapeLink},
                {"relay short link", "https://smp.example.com/r#" + tok, id1, ShapeLink},
                {"one-time short link", "https://smp.example.com/i#" + oneTimeID + "/" + tok, oneTimeID, ShapeLink},
                // page-wrapped full links (the https form the reports quote)
                {"full contact link", "https://simplex.im/contact#/?v=1&smp=" + escURI + "&data=abc", queueID, ShapeLink},
                {"full invitation link", "https://simplex.im/invitation#/?v=1&smp=" + escURI + "&e2e=abc", queueID, ShapeLink},
                {"full link with unescaped smp=", "https://smp6.simplex.im/contact#/?v=2&smp=" + rawURI + "&data=xyz", queueID, ShapeLink},
                {"full link with port and double-escaped tail", "https://simplex.im/contact#/?v=1&smp=smp%3A%2F%2FPQUV2eL0t7OStZOoAsPEV2QYWt4-xilbakvGUGOItUo%3D%40smp6.simplex.im%3A5223%2F" + queueID + "%23%2F%3Fv%3D1%26dh%3DMCowBQYDK2VuAyEALDeVe-sG8mRY22LsXlPgiwTNs9dbiLrNuA7f3ZMAJ2w%3D", queueID, ShapeLink},
                // v1.6.28: the scheme scan is case-insensitive - an
                // upper-case HTTPS:// used to defeat it and land the
                // paste on the generic hint (the wrong ban variant)
                {"upper-case scheme full link", "HTTPS://SIMPLEX.IM/CONTACT#/?v=1&smp=" + escURI + "&data=abc", queueID, ShapeLink},
                {"upper-case scheme short link", "HTTPS://SMP.EXAMPLE.COM/A#" + tok, id1, ShapeLink},
                {"multi-queue link takes the first queue", "https://simplex.im/contact#/?v=1&smp=smp://" + keyHash + "@h1.example.com/AAAAAAAAAAAAAAAAAAAAAAAA#a;smp://" + keyHash + "@h2.example.com/BBBBBBBBBBBBBBBBBBBBBBBB#b", "AAAAAAAAAAAAAAAAAAAAAAAA", ShapeLink},
                {"link inside report text", "abuse report: https://smp.example.com/g#" + tok + " spamming", id1, ShapeLink},
                // bare IDs stay the user-ban path
                {"bare ID", oneTimeID, oneTimeID, ShapeBareID},
                // deliberately rejected forms (v1.6.22)
                {"smp server address", "smp://" + oneTimeID + ":kEy@host.example.com", "", ShapeBareID},
                {"xftp server address", "xftp://" + oneTimeID + ":kEy@host.example.com/file", "", ShapeBareID},
                {"compact simplex:/ form", "simplex:/g#" + tok + "?h=smp.example.com", id1, ShapeLink},
                {"compact simplex:/ contact page form", "simplex:/contact#/?v=1&smp=" + escURI, queueID, ShapeLink},
                {"nothing parses", "not an id at all", "", ShapeBareID},
                {"empty", "", "", ShapeBareID},
        }
        for _, c := range cases {
                gotID, gotShape := ParseTargetIDShape(c.in)
                if gotID != c.wantID || gotShape != c.wantShape {
                        t.Errorf("%s: ParseTargetIDShape = (%q, %v), want (%q, %v)", c.name, gotID, gotShape, c.wantID, c.wantShape)
                }
        }

        // ParseTargetID still returns just the ID
        if got := ParseTargetID("https://smp.example.com/g#" + tok); got != id1 {
                t.Errorf("ParseTargetID(link) = %q, want %q", got, id1)
        }
        if got := ParseTargetID("https://simplex.im/contact#/?v=1&smp=" + escURI); got != queueID {
                t.Errorf("ParseTargetID(full link) = %q, want %q", got, queueID)
        }
        // regression guard: server addresses never yield a target again
        if got := ParseTargetID("smp://" + oneTimeID + ":kEy@host.example.com"); got != "" {
                t.Errorf("ParseTargetID(server address) = %q, want \"\"", got)
        }
}

// TestTargetInputHint: the two known-wrong paste forms get a targeted
// explanation, and everything else the generic prompt (v1.6.22).
func TestTargetInputHint(t *testing.T) {
        if h := TargetInputHint(""); !strings.Contains(h, "Paste what to ban") {
                t.Errorf("empty paste hint = %q", h)
        }
        h := TargetInputHint("smp://PQUV2eL0t7OStZOoAsPEV2QYWt4-xilbakvGUGOItUo=@host.example.com")
        if !strings.Contains(h, "server address") || !strings.Contains(h, "not something you ban") {
                t.Errorf("smp:// hint = %q", h)
        }
        if h := TargetInputHint("xftp://a:key@host/file"); !strings.Contains(h, "server address") {
                t.Errorf("xftp:// hint = %q", h)
        }
        // v1.6.28: the compact-form hint branch is gone - the generic
        // prompt answers it (v1.6.31: well-formed compact links parse
        // now; a malformed one still lands on the generic prompt)
        if h := TargetInputHint("simplex:/g#abc"); !strings.Contains(h, "No link or ID") {
                t.Errorf("simplex:/ paste must get the generic hint now, got %q", h)
        }
        if h := TargetInputHint("hello world"); !strings.Contains(h, "No link or ID") {
                t.Errorf("generic hint = %q", h)
        }
}
