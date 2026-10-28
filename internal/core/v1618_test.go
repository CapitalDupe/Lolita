package core

import (
        "os"
        "path/filepath"
        "strings"
        "testing"
)

// v1.6.18: the official report systems, verified against simplexmq
// stable / v6.5.0 source (Server.hs, Control.hs, Main.hs, Information.hs)
// and the 2024-12-30 content-moderation RFC:
//
//   - XFTP file blocks (the "blocking records for content moderation"
//     of simplexmq 6.3.1) act on FILES: the control port resolves the
//     ID through the recipient map only, the block DELETES the stored
//     chunks at block time and marks the record EntityBlocked, there is
//     NO unblock, and the reason + notice travel to the apps inside the
//     BLOCKED error on every further access.
//   - The report channel to a server operator is the [INFORMATION]
//     complaints contacts pair (complaints_simplex / complaints_email)
//     published on the server's HTTPS info web page.

// TestComplaintsRoundTrip: the complaints contacts are read live from
// the INI, written (or cleared) by SetComplaints, and other keys in the
// section survive the patch. The server is not running here, so no
// restart happens - exactly the "applies at next start" path.
func TestComplaintsRoundTrip(t *testing.T) {
        paths := testPaths(t)
        state, err := NewStateStore(paths)
        if err != nil {
                t.Fatal(err)
        }
        _ = state.UpdateServer(KindXFTP, func(r *ServerRec) {
                r.Installed = true
                r.Enabled = true
                r.Fingerprint = "XFTPFPXFTPFP"
        })
        sv := NewSupervisor(paths, state)
        eng := &Engine{Paths: paths, State: state, Sv: sv, Log: NopInstallLog(),
                Tor: NewTorManager(paths, state, NopInstallLog())}
        eng.Backend = &nativeBackend{eng: eng}

        if err := paths.Ensure(); err != nil {
                t.Fatal(err)
        }
        iniPath := filepath.Join(paths.CfgDir(KindXFTP), "file-server.ini")
        xftpTemplateIni(t, iniPath)

        // fresh install: no complaints address configured
        s, em, err := eng.Complaints(KindXFTP)
        if err != nil {
                t.Fatalf("Complaints: %v", err)
        }
        if s != "" || em != "" {
                t.Fatalf("fresh INI should have no complaints contacts, got %q / %q", s, em)
        }

        // set both (the real full contact link's https form - simplex-chat
        // Library/Internal.hs carries exactly this shape)
        addr := "https://simplex.im/contact#/?v=1&smp=smp%3A%2F%2FPQUV2eL0t7OStZOoAsPEV2QYWt4-xilbakvGUGOItUo%3D%40smp6.simplex.im%2FK1rslx-m5bpXVIdMZg9NLUZ_8JBm8xTt%23MCowBQYDK2VuAyEALDeVe-sG8mRY22LsXlPgiwTNs9dbiLrNuA7f3ZMAJ2w%3D"
        if err := eng.SetComplaints(KindXFTP, addr, "abuse@example.com"); err != nil {
                t.Fatalf("SetComplaints: %v", err)
        }
        s, em, err = eng.Complaints(KindXFTP)
        if err != nil {
                t.Fatalf("Complaints after set: %v", err)
        }
        if s != addr || em != "abuse@example.com" {
                t.Fatalf("round trip = %q / %q, want %q / abuse@example.com", s, em, addr)
        }

        // other keys in [INFORMATION] survive (source_code was there)
        data, err := os.ReadFile(iniPath)
        if err != nil {
                t.Fatal(err)
        }
        low := strings.ToLower(string(data))
        if !strings.Contains(low, "[information]") {
                t.Fatal("the INFORMATION section must survive the patch")
        }
        if !strings.Contains(string(data), complaintsSimplexKey) {
                t.Fatal("complaints_simplex must be in the INI after SetComplaints")
        }

        // clear both (Disable - an empty value must never reach the INI)
        if err := eng.SetComplaints(KindXFTP, "", ""); err != nil {
                t.Fatalf("SetComplaints clear: %v", err)
        }
        s, em, err = eng.Complaints(KindXFTP)
        if err != nil {
                t.Fatalf("Complaints after clear: %v", err)
        }
        if s != "" || em != "" {
                t.Fatalf("cleared contacts should read empty, got %q / %q", s, em)
        }

        // hand-edited values are picked up live (the INI is the truth)
        ini, err := LoadIni(iniPath)
        if err != nil {
                t.Fatal(err)
        }
        ini.Set("INFORMATION", complaintsSimplexKey, "https://example.invalid/i#ID/KEY=")
        if err := os.WriteFile(iniPath, []byte(ini.Text()), 0o600); err != nil {
                t.Fatal(err)
        }
        s, _, err = eng.Complaints(KindXFTP)
        if err != nil || s != "https://example.invalid/i#ID/KEY=" {
                t.Fatalf("hand-edit should be read live, got %q err=%v", s, err)
        }
}

// TestComplaintsValidation: the upstream INI parser makes the server
// FAIL TO START on a malformed complaints_simplex (iniContacts ->
// either error), so sxops must reject bad values before the INI is
// touched. Spaces, a bare server address and garbage emails are out.
// (Expectations corrected in v1.6.19: one-time invitation links are
// NOT valid complaints addresses - the server rejects the
// CMInvitation mode - and the full grammar tests live in v1619_test.go.)
func TestComplaintsValidation(t *testing.T) {
        badSimplex := []string{
                "simplex:/contact#/?v=1&smp=smp%3A%2F%2FPQUV trailing", // space inside
                "smp://fp@example.com",                             // server address, not contact
                "https://example.com/contact",                      // page URL without the address
                "hello",
        }
        for _, s := range badSimplex {
                if err := ValidateComplaintsSimplex(s); err == nil {
                        t.Errorf("ValidateComplaintsSimplex(%q) should reject", s)
                }
        }
        goodSimplex := []string{
                "", // clear
                "https://simplex.im/contact#/?v=1&smp=smp%3A%2F%2FPQUV2eL0t7OStZOoAsPEV2QYWt4-xilbakvGUGOItUo%3D%40smp6.simplex.im%2FK1rslx-m5bpXVIdMZg9NLUZ_8JBm8xTt%23MCowBQYDK2VuAyEALDeVe-sG8mRY22LsXlPgiwTNs9dbiLrNuA7f3ZMAJ2w%3D",
                "https://smp6.simplex.im/a#lrdvu2d8A1GumSmoKb2krQmtKhWXq-tyGpHuM7aMwsw",
        }
        for _, s := range goodSimplex {
                if err := ValidateComplaintsSimplex(s); err != nil {
                        t.Errorf("ValidateComplaintsSimplex(%q) should accept: %v", s, err)
                }
        }
        badEmail := []string{"no at sign", "a b@example.com", "@leading", "trailing@"}
        for _, s := range badEmail {
                if err := ValidateComplaintsEmail(s); err == nil {
                        t.Errorf("ValidateComplaintsEmail(%q) should reject", s)
                }
        }
        for _, s := range []string{"", "abuse@example.com"} {
                if err := ValidateComplaintsEmail(s); err != nil {
                        t.Errorf("ValidateComplaintsEmail(%q) should accept: %v", s, err)
                }
        }
}

// TestComplaintsUnsupportedKinds: only the SMP and XFTP servers parse
// the [INFORMATION] section; the NTF server ignores it (upstream
// serverPublicInfo is only called in the SMP and XFTP mains).
func TestComplaintsUnsupportedKinds(t *testing.T) {
        if !ComplaintsSupported(KindSMP) || !ComplaintsSupported(KindXFTP) {
                t.Fatal("SMP and XFTP must support complaints contacts")
        }
        if ComplaintsSupported(KindNTF) || ComplaintsSupported(KindRelay) {
                t.Fatal("NTF and the relay must not offer complaints contacts")
        }
}

// TestComplaintsNotInstalled: no server files yet -> friendly error,
// never a crash.
func TestComplaintsNotInstalled(t *testing.T) {
        paths := testPaths(t)
        state, err := NewStateStore(paths)
        if err != nil {
                t.Fatal(err)
        }
        sv := NewSupervisor(paths, state)
        eng := &Engine{Paths: paths, State: state, Sv: sv, Log: NopInstallLog(),
                Tor: NewTorManager(paths, state, NopInstallLog())}
        eng.Backend = &nativeBackend{eng: eng}
        if _, _, err := eng.Complaints(KindSMP); err == nil || !strings.Contains(err.Error(), "not installed") {
                t.Errorf("Complaints before install: want friendly error, got %v", err)
        }
        if err := eng.SetComplaints(KindSMP, "https://smp6.simplex.im/a#lrdvu2d8A1GumSmoKb2krQmtKhWXq-tyGpHuM7aMwsw", ""); err == nil || !strings.Contains(err.Error(), "not installed") {
                t.Errorf("SetComplaints before install: want friendly error, got %v", err)
        }
}

// TestBanUnknownFileFriendlyXFTP: an unknown file ID on the file server
// answers "error: AUTH" (upstream getFile SFRecipient), which must read
// as a FILE problem - not a "queue" problem (v1.6.18 wording).
func TestBanUnknownFileFriendlyXFTP(t *testing.T) {
        f := newFakeControlServer(t, true).asXFTP().addFile("RCPRID1")
        rec := &ServerRec{ControlPort: f.port(), ControlAdmin: "tok"}
        out, err := BanClient(KindXFTP, rec, "NOPE", BanReasonSpam, nil)
        if err == nil || !strings.Contains(err.Error(), "no file") {
                t.Errorf("unknown XFTP file: want friendly no-file error, got err=%v out=%q", err, out)
        }
        if strings.Contains(err.Error(), "no queue") {
                t.Errorf("unknown XFTP file must not say no queue: %v", err)
        }
        // the chat server keeps its queue wording
        if err := controlResponseError(KindSMP, "error: AUTH\n"); err == nil || !strings.Contains(err.Error(), "no queue") {
                t.Errorf("SMP unknown queue must keep the queue wording, got %v", err)
        }
}
