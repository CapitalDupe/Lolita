package core

import (
        "path/filepath"
        "strings"
        "testing"
)

// v1.6.19: the complaints address validator is a faithful port of the
// upstream parser (Server/Main.hs iniContacts ->
//   linkP = CLFull <$> connReqUriP' Nothing <|> CLShort <$> strP
// on the ConnectionLink 'CMContact type). Any value the server's parser
// refuses is a FATAL STARTUP ERROR there ("either error") - which is
// exactly how a one-time invitation link in complaints_simplex stopped
// the SMP server in v1.6.18 (its own README recommended "Create one-time
// link"). These tests pin the accepted grammar to the upstream one,
// using the real address shapes the apps produce.
//
// v1.6.28: sxops accepted ONLY the https forms - the belief was that
// the simplex:/ scheme spellings are unused; v1.6.31 reverted that:
// the operator confirmed the apps DO share the compact forms, and they
// parse upstream exactly like the https ones (AConnShortLink strP /
// connReqUriP - the compact short form carries its server in the ?h=
// query, which the upstream parser REQUIRES).

// realFullContact is the full contact link exactly as simplex-chat's
// own source carries it (Library/Internal.hs), in its https page-wrapped
// form: the smp= query value is the percent-escaped queue URI
// smp://<keyHash>@<host>/<queueId>#<dhKey>.
const realFullContact = "https://simplex.im/contact#/?v=1&smp=smp%3A%2F%2FPQUV2eL0t7OStZOoAsPEV2QYWt4-xilbakvGUGOItUo%3D%40smp6.simplex.im%2FK1rslx-m5bpXVIdMZg9NLUZ_8JBm8xTt%23MCowBQYDK2VuAyEALDeVe-sG8mRY22LsXlPgiwTNs9dbiLrNuA7f3ZMAJ2w%3D"

const shortKey = "lrdvu2d8A1GumSmoKb2krQmtKhWXq-tyGpHuM7aMwsw" // 32-byte link key as the app copies it

// TestValidateComplaintsSimplexGood: every https address form the
// server's complaints_simplex parser accepts, in the shapes the apps
// share.
func TestValidateComplaintsSimplexGood(t *testing.T) {
        good := []string{
                "", // clear the key
                realFullContact,
                // the same full link unescaped (both spellings parse upstream)
                "https://simplex.im/contact#/?v=2&smp=smp://PQUV2eL0t7OStZOoAsPEV2QYWt4-xilbakvGUGOItUo=@smp6.simplex.im/K1rslx-m5bpXVIdMZg9NLUZ_8JBm8xTt#MCowBQYDK2VuAyEALDeVe-sG8mRY22LsXlPgiwTNs9dbiLrNuA7f3ZMAJ2w=",
                // the versioned queue form (port + /?v=..&dh=.. after the queue ID)
                "https://simplex.im/contact#/?v=1&smp=smp%3A%2F%2FPQUV2eL0t7OStZOoAsPEV2QYWt4-xilbakvGUGOItUo%3D%40smp6.simplex.im%3A5223%2FK1rslx-m5bpXVIdMZg9NLUZ_8JBm8xTt%23%2F%3Fv%3D1%26dh%3DMCowBQYDK2VuAyEALDeVe-sG8mRY22LsXlPgiwTNs9dbiLrNuA7f3ZMAJ2w%3D",
                // short contact address, https form (the simplex-chat README's own address)
                "https://smp6.simplex.im/a#" + shortKey,
                // the other durable CMContact link types (channel / group / relay), https forms
                "https://smp6.simplex.im/c#" + shortKey,
                "https://smp6.simplex.im/g#" + shortKey,
                "https://smp6.simplex.im/r#" + shortKey,
                // uppercase type letter, optional "/" before "#", port in the query
                "https://smp6.simplex.im/A/#" + shortKey + "?p=5223",
                // v1.6.31: the compact simplex:/ spellings of the same
                // durable contact forms (the ?h= param carries the
                // server - required on the compact short form)
                "simplex:/contact#/?v=1&smp=smp%3A%2F%2FPQUV2eL0t7OStZOoAsPEV2QYWt4-xilbakvGUGOItUo%3D%40smp6.simplex.im%2FK1rslx-m5bpXVIdMZg9NLUZ_8JBm8xTt%23MCowBQYDK2VuAyEALDeVe-sG8mRY22LsXlPgiwTNs9dbiLrNuA7f3ZMAJ2w%3D",
                "simplex:/a#" + shortKey + "?h=smp6.simplex.im",
                "simplex:/c#" + shortKey + "?h=smp6.simplex.im&p=5223",
        }
        for _, s := range good {
                if err := ValidateComplaintsSimplex(s); err != nil {
                        t.Errorf("ValidateComplaintsSimplex(%q) should accept: %v", s, err)
                }
        }
}

// TestValidateComplaintsSimplexInvitation: one-time invitation links
// are the CMInvitation connection mode - the server's complaints parser
// refuses them ("bad connection request mode") and the server does not
// start. v1.6.18 accepted and recommended them; v1.6.19 must refuse
// every invitation spelling with an actionable message.
func TestValidateComplaintsSimplexInvitation(t *testing.T) {
        invitations := []string{
                // full one-time link (with its e2e= parameter)
                "https://simplex.im/invitation#/?v=1&smp=smp%3A%2F%2FPQUV2eL0t7OStZOoAsPEV2QYWt4-xilbakvGUGOItUo%3D%40smp6.simplex.im%2FK1rslx-m5bpXVIdMZg9NLUZ_8JBm8xTt%23MCowBQYDK2VuAyEALDeVe-sG8mRY22LsXlPgiwTNs9dbiLrNuA7f3ZMAJ2w%3D&e2e=abc",
                // short one-time link (link ID and key after #)
                "https://smp6.simplex.im/i#" + shortKey + "/" + shortKey,
        }
        for _, s := range invitations {
                err := ValidateComplaintsSimplex(s)
                if err == nil {
                        t.Errorf("ValidateComplaintsSimplex(%q) must reject one-time invitation links", s)
                        continue
                }
                if !strings.Contains(err.Error(), "one-time") {
                        t.Errorf("invitation rejection should explain the one-time problem, got: %v", err)
                }
        }
}

// TestValidateComplaintsSimplexBad: values the validator refuses -
// each would stop the server from starting. The compact simplex:/
// forms are validated with the same grammar as the https ones
// (v1.6.31): malformed compact spellings and the compact short form
// WITHOUT its ?h= server param are rejected; only a well-formed
// compact contact address passes (see TestValidateComplaintsSimplexGood).
func TestValidateComplaintsSimplexBad(t *testing.T) {
        bad := map[string]string{
                "smp://fp@example.com":  "a server address, not a contact address",
                "xftp://fp@example.com": "a file-server address",
                // malformed https spellings
                "https://example.com/contact":                   "page URL without the address",
                "https://example.com/contact#/?smp=only":        "full link missing v=",
                "https://example.com/contact#/?v=2":             "full link missing smp=",
                "https://example.com/contact#/?v=x&smp=smp%3A%2F%2FPQUV2eL0t7OStZOoAsPEV2QYWt4-xilbakvGUGOItUo%3D%40smp6.simplex.im%2FK1rslx-m5bpXVIdMZg9NLUZ_8JBm8xTt%23MCowBQYDK2VuAyEALDeVe-sG8mRY22LsXlPgiwTNs9dbiLrNuA7f3ZMAJ2w%3D": "v= is not a version range",
                "https://example.com/contact#/?v=2&smp=not-a-queue":       "smp= is not a queue URI",
                "https://example.com/contact#/?v=2&smp=smp%3A%2F%2Fbroken": "smp= queue missing @host",
                "https://example.com/x#" + shortKey:                       "unknown short link type letter",
                "https://example.com/a#short":                              "key is not base64url",
                "https://example.com/a#" + shortKey + "?p=port":            "port is not a number",
                // malformed / refused compact spellings (v1.6.31 grammar):
                // the short form without its ?h= server part is a parse
                // failure upstream ("short link without server")
                "simplex:/a#" + shortKey:      "compact scheme (short, without the required ?h=)",
                "simplex:/contact#":           "compact scheme (no #/? query at all)",
                "simplex:/contact#/?smp=only": "compact scheme (full link missing v=)",
                "simplex:/contact#/?v=2":      "compact scheme (full link missing smp=)",
                "simplex:/contact#/?v=x&smp=smp%3A%2F%2FPQUV2eL0t7OStZOoAsPEV2QYWt4-xilbakvGUGOItUo%3D%40smp6.simplex.im%2FK1rslx-m5bpXVIdMZg9NLUZ_8JBm8xTt%23MCowBQYDK2VuAyEALDeVe-sG8mRY22LsXlPgiwTNs9dbiLrNuA7f3ZMAJ2w%3D": "compact scheme (v= is not a version range)",
                "simplex:/contact#/?v=2&smp=not-a-queue":       "compact scheme (smp= is not a queue URI)",
                "simplex:/contact#/?v=2&smp=smp%3A%2F%2Fbroken": "compact scheme (smp= queue missing @host)",
                "simplex:/x#" + shortKey + "?h=h":               "compact scheme (unknown short link type letter)",
                "simplex:/a#short?h=h":                          "compact scheme (key is not base64url)",
                "simplex:/a#" + shortKey + "?h=h&p=port":        "compact scheme (port is not a number)",
                "simplex:/invitation#/?v=1&smp=x":               "compact scheme (invitation - wrong mode)",
                "simplex:/i#" + shortKey + "/" + shortKey + "?h=smp6.simplex.im": "compact scheme (short invitation - wrong mode)",
                "hello": "not an address",
        }
        for s, why := range bad {
                if err := ValidateComplaintsSimplex(s); err == nil {
                        t.Errorf("ValidateComplaintsSimplex(%q) should reject: %s", s, why)
                }
        }
}

// TestComplaintsSimplexForm: the save result tells the operator which
// form was recognized (v1.6.31: the compact spellings report their
// forms too - they are valid saves).
func TestComplaintsSimplexForm(t *testing.T) {
        cases := map[string]string{
                realFullContact:                                 "full contact link",
                "https://smp6.simplex.im/a#" + shortKey:         "contact address (short link)",
                "https://smp6.simplex.im/c#" + shortKey:         "channel address (short link)",
                "https://smp6.simplex.im/g#" + shortKey:         "group link (short link)",
                "https://smp6.simplex.im/r#" + shortKey:         "relay link (short link)",
                "simplex:/a#" + shortKey + "?h=smp6.simplex.im": "contact address (short link)", // v1.6.31: valid -> the same form
                "https://smp6.simplex.im/i#" + shortKey + "/" + shortKey: "", // invalid -> no form
                "":                                              "",
        }
        for s, want := range cases {
                if got := ComplaintsSimplexForm(s); got != want {
                        t.Errorf("ComplaintsSimplexForm(%q) = %q, want %q", s, got, want)
                }
        }
}

// TestComplaintsSaveShortAddress: the https short form the app copies
// for "your SimpleX address" was WRONGLY rejected by v1.6.18 (which
// required the simplex:/ prefix) - it must save now, and the INI must
// round-trip it unchanged.
func TestComplaintsSaveShortAddress(t *testing.T) {
        paths := testPaths(t)
        state, err := NewStateStore(paths)
        if err != nil {
                t.Fatal(err)
        }
        _ = state.UpdateServer(KindSMP, func(r *ServerRec) {
                r.Installed = true
                r.Enabled = true
                r.Fingerprint = "SMPFPSMPFPSM"
        })
        sv := NewSupervisor(paths, state)
        eng := &Engine{Paths: paths, State: state, Sv: sv, Log: NopInstallLog(),
                Tor: NewTorManager(paths, state, NopInstallLog())}
        eng.Backend = &nativeBackend{eng: eng}

        if err := paths.Ensure(); err != nil {
                t.Fatal(err)
        }
        smpTemplateIni(t, filepath.Join(paths.CfgDir(KindSMP), "smp-server.ini"))

        addr := "https://smp6.simplex.im/a#" + shortKey
        if err := eng.SetComplaints(KindSMP, addr, ""); err != nil {
                t.Fatalf("SetComplaints(https short address): %v", err)
        }
        s, _, err := eng.Complaints(KindSMP)
        if err != nil {
                t.Fatal(err)
        }
        if s != addr {
                t.Fatalf("round trip = %q, want %q", s, addr)
        }
}

// TestComplaintsRejectInvitationBeforeINI: the value that crashed the
// SMP server in v1.6.18 (a one-time invitation link) never reaches the
// INI now - SetComplaints refuses it and the file is untouched.
func TestComplaintsRejectInvitationBeforeINI(t *testing.T) {
        paths := testPaths(t)
        state, err := NewStateStore(paths)
        if err != nil {
                t.Fatal(err)
        }
        _ = state.UpdateServer(KindSMP, func(r *ServerRec) {
                r.Installed = true
                r.Enabled = true
                r.Fingerprint = "SMPFPSMPFPSM"
        })
        sv := NewSupervisor(paths, state)
        eng := &Engine{Paths: paths, State: state, Sv: sv, Log: NopInstallLog(),
                Tor: NewTorManager(paths, state, NopInstallLog())}
        eng.Backend = &nativeBackend{eng: eng}

        if err := paths.Ensure(); err != nil {
                t.Fatal(err)
        }
        iniPath := filepath.Join(paths.CfgDir(KindSMP), "smp-server.ini")
        smpTemplateIni(t, iniPath)

        // the exact trap from v1.6.18: a one-time link (the https short
        // form a paste carries today)
        if err := eng.SetComplaints(KindSMP, "https://smp6.simplex.im/i#"+shortKey+"/"+shortKey, ""); err == nil {
                t.Fatal("SetComplaints must refuse a one-time invitation link")
        } else if !strings.Contains(err.Error(), "one-time") {
                t.Fatalf("the rejection should explain the one-time problem, got: %v", err)
        }

        // nothing was written
        s, _, err := eng.Complaints(KindSMP)
        if err != nil {
                t.Fatal(err)
        }
        if s != "" {
                t.Fatalf("a refused address must not reach the INI, got %q", s)
        }
}
