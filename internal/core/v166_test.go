package core

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// v1.6.6: the chat relay must never touch official SimpleX servers, even
// while its own SMP server is unusable. Two gaps were closed:
//
//   - the relay PROTOCOL TEST ran its one-shot CLI without the -s lock,
//     so the test agent created its own connections on the CLI's preset
//     servers (smp4.simplex.im and friends) whenever it picked a server
//     itself - with the own SMP server disabled that surfaced as
//     "protocol test failed at RTSGetLink ... smp4.simplex.im". Every
//     test run is now locked to the own SMP server and uses a fresh
//     throw-away profile (the old persistent one may hold a user from
//     the unlocked era and is removed on the first run).
//   - the own-SMP stop guard only looked at the recorded ADDRESS: with
//     the SMP server merely disabled or stopped the relay kept running.
//     The guard (and the relay test) now require the own SMP server to
//     be installed, enabled and running.

// stubChatBin installs a fake simplex-chat CLI for kind k that records
// its execution in marker and prints one line of output.
func stubChatBin(t *testing.T, paths *Paths, k Kind, marker, printed string) {
	t.Helper()
	if err := paths.Ensure(); err != nil {
		t.Fatal(err)
	}
	bin := paths.BinPath(k)
	if err := os.MkdirAll(filepath.Dir(bin), 0o700); err != nil {
		t.Fatal(err)
	}
	script := "#!/bin/sh\nprintf x > '" + marker + "'\nprintf '%s\\n' '" + printed + "'\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
}

// lastTestArgv returns the recorded argv line of the last CLI one-shot.
func lastTestArgv(t *testing.T, paths *Paths) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(paths.TestDir(), "last-test.txt"))
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimPrefix(strings.SplitN(string(data), "\n", 2)[0], "argv: ")
}

// newV166Engine builds an engine on a public-host deployment (no Tor
// needed for the tests below).
func newV166Engine(t *testing.T) (*Engine, *StateStore, *Supervisor, *Paths) {
	t.Helper()
	paths := testPaths(t)
	state, err := NewStateStore(paths)
	if err != nil {
		t.Fatal(err)
	}
	if err := state.SetSettings(Settings{PublicHost: "example.com"}); err != nil {
		t.Fatal(err)
	}
	sv := NewSupervisor(paths, state)
	eng := &Engine{Paths: paths, State: state, Sv: sv, Log: NopInstallLog(),
		Tor: NewTorManager(paths, state, NopInstallLog())}
	return eng, state, sv, paths
}

func v166SMPRunning(t *testing.T, state *StateStore, sv *Supervisor, paths *Paths) {
	t.Helper()
	_ = state.UpdateServer(KindSMP, func(r *ServerRec) {
		r.Installed = true
		r.Enabled = true
		r.Fingerprint = "FPFPFPFPFPFP"
	})
	stubServerProc(t, sv, paths, KindSMP)
}

func TestServerTestLockedToOwnSMP(t *testing.T) {
	eng, state, sv, paths := newV166Engine(t)
	marker := filepath.Join(t.TempDir(), "ran")
	// the protocol test runs the simplex-chat CLI (the relay-kind binary)
	stubChatBin(t, paths, KindRelay, marker, "server test passed")
	v166SMPRunning(t, state, sv, paths)
	// a stale pre-1.6.6 persistent self-test profile must be dropped
	if err := os.MkdirAll(paths.TestDBPrefix(), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(paths.TestDBPrefix(), "db"), []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}

	res := eng.RunServerTest(KindSMP)
	if !res.Passed {
		t.Fatalf("SMP protocol test failed: %+v", res)
	}
	argv := lastTestArgv(t, paths)
	if !strings.Contains(argv, `"-s" "smp://FPFPFPFPFPFP@example.com"`) {
		t.Errorf("test argv not locked to the own SMP server: %s", argv)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Errorf("the CLI one-shot never ran (marker missing): %v", err)
	}
	if _, err := os.Stat(paths.TestDBPrefix()); !os.IsNotExist(err) {
		t.Errorf("the legacy persistent self-test profile survived: %v", err)
	}
}

func TestRelayTestLockedToOwnSMP(t *testing.T) {
	eng, state, sv, paths := newV166Engine(t)
	marker := filepath.Join(t.TempDir(), "ran")
	stubChatBin(t, paths, KindRelay, marker, "relay test passed")
	v166SMPRunning(t, state, sv, paths)
	_ = state.UpdateServer(KindRelay, func(r *ServerRec) {
		r.Installed = true
		r.Enabled = true
		r.Fingerprint = "https://example.com/r#KEYKEYKEYKEY"
	})

	res := eng.RunServerTest(KindRelay)
	if !res.Passed {
		t.Fatalf("relay protocol test failed: %+v", res)
	}
	argv := lastTestArgv(t, paths)
	if !strings.Contains(argv, `"-s" "smp://FPFPFPFPFPFP@example.com"`) {
		t.Errorf("relay test argv not locked to the own SMP server: %s", argv)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Errorf("the CLI one-shot never ran (marker missing): %v", err)
	}
}

func TestRelayTestSkippedWhenOwnSMPDisabled(t *testing.T) {
	eng, state, _, paths := newV166Engine(t)
	marker := filepath.Join(t.TempDir(), "ran")
	stubChatBin(t, paths, KindRelay, marker, "relay test passed")
	_ = state.UpdateServer(KindSMP, func(r *ServerRec) {
		r.Installed = true
		r.Enabled = false // the user disabled the own SMP server
		r.Fingerprint = "FPFPFPFPFPFP"
		r.OnionHost = "myownhostxyz1234567890abc.onion"
	})
	_ = state.UpdateServer(KindRelay, func(r *ServerRec) {
		r.Installed = true
		r.Enabled = true
		r.Fingerprint = "https://example.com/r#KEYKEYKEYKEY"
	})

	res := eng.RunServerTest(KindRelay)
	if res.Passed {
		t.Fatal("relay test ran although the own SMP server is disabled")
	}
	if !strings.Contains(res.Err, "disabled") {
		t.Errorf("skip reason does not mention the disabled SMP server: %q", res.Err)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Error("the CLI ran although the test should have been skipped (official servers could be contacted)")
	}
}

func TestRelayTestSkippedWhenOwnSMPNotRunning(t *testing.T) {
	eng, state, _, paths := newV166Engine(t)
	marker := filepath.Join(t.TempDir(), "ran")
	stubChatBin(t, paths, KindRelay, marker, "relay test passed")
	// installed and enabled, but no process running
	_ = state.UpdateServer(KindSMP, func(r *ServerRec) {
		r.Installed = true
		r.Enabled = true
		r.Fingerprint = "FPFPFPFPFPFP"
		r.OnionHost = "myownhostxyz1234567890abc.onion"
	})
	_ = state.UpdateServer(KindRelay, func(r *ServerRec) {
		r.Installed = true
		r.Enabled = true
		r.Fingerprint = "https://example.com/r#KEYKEYKEYKEY"
	})

	res := eng.RunServerTest(KindRelay)
	if res.Passed {
		t.Fatal("relay test ran although the own SMP server is not running")
	}
	if !strings.Contains(res.Err, "not running") {
		t.Errorf("skip reason does not mention the stopped SMP server: %q", res.Err)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Error("the CLI ran although the test should have been skipped (official servers could be contacted)")
	}
}

func TestStopRelayWhenOwnSMPDisabled(t *testing.T) {
	paths := testPaths(t)
	state, err := NewStateStore(paths)
	if err != nil {
		t.Fatal(err)
	}
	sv := NewSupervisor(paths, state)
	eng := &Engine{Paths: paths, State: state, Sv: sv, Log: NopInstallLog()}
	stubRelayBin(t, paths)
	// the address is fully known (recorded onion host) - only the server
	// itself is disabled: the v1.6.4 guard would have let the relay run.
	_ = state.UpdateServer(KindSMP, func(r *ServerRec) {
		r.Installed = true
		r.Enabled = false
		r.Fingerprint = "FPFPFPFPFPFP"
		r.OnionHost = "myownhostxyz1234567890abc.onion"
	})
	_ = state.UpdateServer(KindRelay, func(r *ServerRec) {
		r.Installed = true
		r.Enabled = true
	})

	if err := sv.StartWithArgs(KindRelay, []string{"--relay", "--headless"}, nil); err != nil {
		t.Fatal(err)
	}
	if !eng.StopRelayIfOwnSMPMissing() {
		t.Fatal("guard did not stop the relay although the own SMP server is disabled")
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
