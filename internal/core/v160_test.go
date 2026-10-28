package core

import (
        "encoding/json"
        "os"
        "path/filepath"
        "strings"
        "testing"
)

// v1.6.0: the chat relay CLI is LOCKED to the own SMP server. simplex-chat's
// -s/--server option REPLACES the CLI's whole per-user server list for the
// run, so the relay can never create a queue or connection on the preset
// (official SimpleX) servers - everything lands on the own server directly.
// The one-shot registration stays pinned via --relay-address-server as well.
func TestRelayArgsLockToOwnServer(t *testing.T) {
        own := "smp://FP@pub.example.com,abc123.onion"
        plan := relayPlanSync(Settings{}, own, 9055)
        argv := relayArgsWith(Settings{}, &Paths{}, plan)
        joined := strings.Join(argv, " ")
        if !strings.Contains(joined, "-s "+own) {
                t.Errorf("relay argv must lock the CLI to the own SMP server via -s: %v", argv)
        }
        if !strings.Contains(joined, "--relay-address-server "+own) {
                t.Errorf("relay argv must pin the address registration to the own server: %v", argv)
        }
        // public-host deployments are locked the same way (no SOCKS needed)
        pub := "smp://FP@srv.example.com"
        argv = relayArgsWith(Settings{}, &Paths{}, relayPlanSync(Settings{}, pub, 0))
        if !strings.Contains(strings.Join(argv, " "), "-s "+pub) {
                t.Errorf("public-host relay argv must carry -s too: %v", argv)
        }
        // no own address -> neither flag (the engine refuses to start anyway)
        argv = relayArgsWith(Settings{}, &Paths{}, relayPlanSync(Settings{}, "", 0))
        if strings.Contains(strings.Join(argv, " "), "-s ") || strings.Contains(strings.Join(argv, " "), "--relay-address-server") {
                t.Errorf("without an own SMP address no server flags may be passed: %v", argv)
        }
}

// v1.6.0: when an own XFTP server is installed, the relay's file uploads are
// locked to it via --xftp-server (the CLI's preset XFTP list is replaced).
func TestRelayArgsOwnXFTPServer(t *testing.T) {
        base := relayPlanSync(Settings{}, "smp://FP@abc.onion", 9050)
        base.XFTP = "xftp://XF@files.example.com,fff.onion"
        joined := strings.Join(relayArgsWith(Settings{}, &Paths{}, base), " ")
        if !strings.Contains(joined, "--xftp-server xftp://XF@files.example.com,fff.onion") {
                t.Errorf("relay argv must lock file uploads to the own XFTP server: %s", joined)
        }
        // no own XFTP server -> flag omitted
        joined = strings.Join(relayArgsWith(Settings{}, &Paths{}, base), " ")
        noXFTP := base
        noXFTP.XFTP = ""
        joined = strings.Join(relayArgsWith(Settings{}, &Paths{}, noXFTP), " ")
        if strings.Contains(joined, "--xftp-server") {
                t.Errorf("no own XFTP address -> no --xftp-server flag: %s", joined)
        }
        // placeholder address -> flag omitted (half-configured system)
        ph := base
        ph.XFTP = "xftp://XF@YOUR-SERVER-HOST"
        joined = strings.Join(relayArgsWith(Settings{}, &Paths{}, ph), " ")
        if strings.Contains(joined, "--xftp-server") {
                t.Errorf("placeholder XFTP address -> no --xftp-server flag: %s", joined)
        }
}

// v1.6.0: one-shot CLI runs against the relay profile (/sa recovery, relay
// commands) are locked to the own SMP server via -s as well - no sxops
// managed CLI run ever consults the preset (official) servers.
func TestOneShotRelayArgsLockToOwnServer(t *testing.T) {
        own := "smp://FP@abc123.onion"
        argv := oneShotRelayArgs("/data/relay", own, 9055, "/sa")
        joined := strings.Join(argv, " ")
        if !strings.Contains(joined, "-s "+own) {
                t.Errorf("one-shot argv must lock the run to the own SMP server: %v", argv)
        }
        if !strings.Contains(joined, "--socks-proxy 127.0.0.1:9055") || !strings.Contains(joined, "-e /sa") {
                t.Errorf("one-shot argv must keep SOCKS routing and the command: %v", argv)
        }
        // no known own address -> -s omitted, SOCKS default kept
        argv = oneShotRelayArgs("/data/relay", "", 0, "/sa")
        joined = strings.Join(argv, " ")
        if strings.Contains(joined, "-s ") {
                t.Errorf("unknown own address -> no -s: %v", argv)
        }
        if !strings.Contains(joined, "-x") {
                t.Errorf("unknown SOCKS port -> the -x default must stay: %v", argv)
        }
        // placeholder address -> -s omitted
        argv = oneShotRelayArgs("/data/relay", "smp://FP@YOUR-SERVER-HOST", 9055, "/sa")
        if strings.Contains(strings.Join(argv, " "), "-s smp://FP@YOUR-SERVER-HOST") {
                t.Errorf("placeholder own address -> no -s: %v", argv)
        }
}

// v1.6.0: the automatic foreign-link re-registration is GONE - a legacy
// state file that still carries the v1.5.9 relay_foreign_reset_done flag
// loads fine, the flag is dropped on the next save, and no State field
// references it anymore.
func TestOldStateForeignResetFlagIgnored(t *testing.T) {
        dir := t.TempDir()
        paths, err := NewPaths(dir)
        if err != nil {
                t.Fatal(err)
        }
        old := `{"version":1,"settings":{"relay_name":"R","relay_use_own_smp":true},` +
                `"servers":{"relay":{"kind":"relay","enabled":true,"installed":true}},` +
                `"relay_foreign_reset_done":true}`
        if err := os.WriteFile(filepath.Join(dir, "state.json"), []byte(old), 0o600); err != nil {
                t.Fatal(err)
        }
        st, err := NewStateStore(paths)
        if err != nil {
                t.Fatal(err)
        }
        if st.Get().Settings.RelayName != "R" {
                t.Fatalf("legacy state must load: %+v", st.Get())
        }
        if err := st.Save(); err != nil {
                t.Fatal(err)
        }
        data, err := os.ReadFile(paths.StateFile())
        if err != nil {
                t.Fatal(err)
        }
        var doc map[string]json.RawMessage
        if err := json.Unmarshal(data, &doc); err != nil {
                t.Fatal(err)
        }
        if _, ok := doc["relay_foreign_reset_done"]; ok {
                t.Error("the dropped v1.5.9 flag must not be re-persisted")
        }
}
