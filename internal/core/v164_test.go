package core

import (
        "os"
        "path/filepath"
        "testing"
        "time"
)

// v1.6.4: a chat relay still running while the own SMP server is not
// found (address absent or placeholder) is STOPPED, not left running.
// sxops can only START the relay locked to the own SMP server
// (v1.6.0/v1.6.1) - a relay adopted from an older sxops run has no such
// lock, and its CLI falls back to the official SimpleX servers whenever
// it picks a server itself. The guard closes that last gap.

// stubRelayBin installs a fake simplex-chat CLI that just sleeps, so the
// supervisor can start/stop a real process for the guard tests.
func stubRelayBin(t *testing.T, paths *Paths) {
        t.Helper()
        if err := paths.Ensure(); err != nil {
                t.Fatal(err)
        }
        bin := paths.BinPath(KindRelay)
        if err := os.MkdirAll(filepath.Dir(bin), 0o700); err != nil {
                t.Fatal(err)
        }
        if err := os.WriteFile(bin, []byte("#!/bin/sh\nsleep 120\n"), 0o755); err != nil {
                t.Fatal(err)
        }
}

// stubServerProc installs a sleeping stub for a server kind and starts
// it under the supervisor, so Sv.Status reports it as RUNNING (v1.6.6:
// the own-SMP relay guard and the relay protocol test require the own
// SMP server to be up, not merely recorded).
func stubServerProc(t *testing.T, sv *Supervisor, paths *Paths, k Kind) {
        t.Helper()
        if err := paths.Ensure(); err != nil {
                t.Fatal(err)
        }
        bin := paths.BinPath(k)
        if err := os.MkdirAll(filepath.Dir(bin), 0o700); err != nil {
                t.Fatal(err)
        }
        if err := os.WriteFile(bin, []byte("#!/bin/sh\nsleep 120\n"), 0o755); err != nil {
                t.Fatal(err)
        }
        if err := sv.StartWithArgs(k, StartArgs(k), nil); err != nil {
                t.Fatal(err)
        }
        t.Cleanup(func() { _ = sv.Stop(k, 5*time.Second) })
}

// setGuardServers records an installed SMP+relay pair; smpOnionHost ""
// leaves the SMP address at the placeholder (own SMP server not found).
func setGuardServers(t *testing.T, st *StateStore, smpOnionHost string) {
        t.Helper()
        _ = st.UpdateServer(KindSMP, func(r *ServerRec) {
                r.Installed = true
                r.Enabled = true
                r.Fingerprint = "FPFPFPFPFPFP"
                r.OnionHost = smpOnionHost
        })
        _ = st.UpdateServer(KindRelay, func(r *ServerRec) {
                r.Installed = true
                r.Enabled = true
        })
}

func TestStopRelayIfOwnSMPMissing(t *testing.T) {
        paths := testPaths(t)
        state, err := NewStateStore(paths)
        if err != nil {
                t.Fatal(err)
        }
        sv := NewSupervisor(paths, state)
        eng := &Engine{Paths: paths, State: state, Sv: sv, Log: NopInstallLog()}
        stubRelayBin(t, paths)
        // SMP installed but no onion host and no public host -> Address() is
        // the placeholder -> own SMP server not found.
        setGuardServers(t, state, "")

        if err := sv.StartWithArgs(KindRelay, []string{"--relay", "--headless"}, nil); err != nil {
                t.Fatal(err)
        }
        if sv.Status(KindRelay).State != ProcRunning {
                t.Fatal("relay stub did not start")
        }
        if !eng.StopRelayIfOwnSMPMissing() {
                t.Fatal("guard did not report stopping the relay")
        }
        deadline := time.Now().Add(10 * time.Second)
        for time.Now().Before(deadline) {
                switch sv.Status(KindRelay).State {
                case ProcRunning, ProcStarting, ProcStopping:
                        time.Sleep(100 * time.Millisecond)
                        continue
                }
                break
        }
        if st := sv.Status(KindRelay).State; st == ProcRunning || st == ProcStarting {
                t.Fatalf("relay still %v after the guard ran", st)
        }
}

func TestStopRelayIfOwnSMPMissingKeepsRelayWithSMP(t *testing.T) {
        paths := testPaths(t)
        state, err := NewStateStore(paths)
        if err != nil {
                t.Fatal(err)
        }
        sv := NewSupervisor(paths, state)
        eng := &Engine{Paths: paths, State: state, Sv: sv, Log: NopInstallLog()}
        stubRelayBin(t, paths)
        setGuardServers(t, state, "myownhostxyz1234567890abc.onion")
        // v1.6.6: a KNOWN address is not enough - the own SMP server
        // must actually be running for the relay to be allowed to run.
        stubServerProc(t, sv, paths, KindSMP)

        if err := sv.StartWithArgs(KindRelay, []string{"--relay", "--headless"}, nil); err != nil {
                t.Fatal(err)
        }
        if eng.StopRelayIfOwnSMPMissing() {
                t.Fatal("guard stopped the relay although the own SMP server is known and running")
        }
        if st := sv.Status(KindRelay).State; st != ProcRunning {
                t.Fatalf("relay should keep running, state %v", st)
        }
        _ = sv.Stop(KindRelay, 5*time.Second)
}

// Statuses() enforces the rule on every poll: the relay row comes back
// stopped (with the explaining note) when the own SMP server is missing.
func TestStatusesStopsRelayWithoutOwnSMP(t *testing.T) {
        paths := testPaths(t)
        state, err := NewStateStore(paths)
        if err != nil {
                t.Fatal(err)
        }
        sv := NewSupervisor(paths, state)
        eng := &Engine{Paths: paths, State: state, Sv: sv, Log: NopInstallLog(),
                Tor: NewTorManager(paths, state, NopInstallLog())}
        eng.Backend = newPlatformBackend(eng)
        stubRelayBin(t, paths)
        setGuardServers(t, state, "")
        // an http relay link keeps the /sa recovery path out of the test
        _ = state.UpdateServer(KindRelay, func(r *ServerRec) {
                r.Fingerprint = "https://myownhostxyz1234567890abc.onion/r#KEYKEYKEYKEY"
        })

        if err := sv.StartWithArgs(KindRelay, []string{"--relay", "--headless"}, nil); err != nil {
                t.Fatal(err)
        }
        var relayRow *ComponentStatus
        rows := eng.Statuses()
        for i := range rows {
                if rows[i].Kind == KindRelay {
                        relayRow = &rows[i]
                }
        }
        if relayRow == nil {
                t.Fatal("no relay row in Statuses")
        }
        if relayRow.Running {
                t.Fatal("Statuses left the relay running without the own SMP server")
        }
        if relayRow.Note == "" {
                t.Error("relay row carries no note explaining why it is stopped")
        }
}
