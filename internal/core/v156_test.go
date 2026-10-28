package core

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

// TestRelaySocksOfPlan pins the SOCKS-port recording contract of v1.5.6:
// the watchdog compares TorRec.RelaySocks against ClientSocksPortOf, so
// the recorded value must be exactly what the relay's argv implies.
func TestRelaySocksOfPlan(t *testing.T) {
	cases := []struct {
		name string
		plan RelayPlan
		want int
	}{
		{"onion with explicit port", RelayPlan{Register: "smp://fp@aaaa.onion:5223", Socks: 9150}, 9150},
		{"onion via -x default", RelayPlan{Register: "smp://fp@aaaa.onion:5223", Socks: 0}, 9050},
		{"public host needs no proxy", RelayPlan{Register: "smp://fp@example.com:5223"}, -1},
		{"no registration", RelayPlan{}, -1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := relaySocksOfPlan(tc.plan); got != tc.want {
				t.Fatalf("relaySocksOfPlan(%+v) = %d, want %d", tc.plan, got, tc.want)
			}
		})
	}
}

// TestRelaySocksDrifted pins the drift decision - including the guard
// that keeps public-host relays (recorded -1) from flapping forever and
// adopted relays (recorded 0) from being restarted blindly.
func TestRelaySocksDrifted(t *testing.T) {
	onion := "smp://fp@aaaa.onion:5223"
	public := "smp://fp@example.com:5223"
	cases := []struct {
		name     string
		smpAddr  string
		recorded int
		want     int
		drifted  bool
	}{
		{"drifted ports on onion", onion, 9050, 9150, true},
		{"healthy onion", onion, 9150, 9150, false},
		{"public host never drifts", public, -1, 9150, false},
		{"adopted relay not compared", onion, 0, 9150, false},
		{"tor port unknown", onion, 9050, 0, false},
		{"relay without proxy on onion addr", onion, -1, 9150, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := relaySocksDrifted(tc.smpAddr, tc.recorded, tc.want); got != tc.drifted {
				t.Fatalf("relaySocksDrifted(%q, %d, %d) = %v, want %v",
					tc.smpAddr, tc.recorded, tc.want, got, tc.drifted)
			}
		})
	}
}

// TestProbeOnionsAsyncUsesClientPort pins the v1.5.6 fix for the
// Dashboard's reachability verdicts in single-hop mode: the service Tor
// instance runs with SocksPort 0 (or a dead port), so probing through
// Tor.SocksPort would show "not reachable" on a perfectly working onion.
// The probe must go through ClientSocksPortOf (the client instance).
func TestProbeOnionsAsyncUsesClientPort(t *testing.T) {
	eng, paths, st := newTestEngine(t)
	if err := st.UpdateServer(KindSMP, func(r *ServerRec) {
		r.Installed = true
		r.OnionHost = "aaaaaaaa.onion"
		r.Port = 5223
	}); err != nil {
		t.Fatal(err)
	}
	up := fakeSocksServer(t, 0x00, false)  // the client instance port
	dead := fakeSocksServer(t, 0x01, true) // never answers: the old code's port

	// single-hop: state carries a dead service SOCKS port and a live
	// client SOCKS port; the pidfile makes TorManager.Running() true.
	if err := st.With(func(s *State) {
		s.Tor.SocksPort = dead
		s.Tor.ClientSocksPort = up
		s.Settings.SingleHopOnion = true
	}); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(paths.TorPidFile()), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(paths.TorPidFile(), []byte(strconv.Itoa(os.Getpid())), 0o600); err != nil {
		t.Fatal(err)
	}

	infos := eng.ProbeOnions()
	if len(infos) != 1 {
		t.Fatalf("probe count = %d, want 1", len(infos))
	}
	if !infos[0].Reachable {
		t.Fatalf("SMP onion probed as not reachable (%v) - the probe must use the CLIENT SOCKS port in single-hop mode", infos[0].Err)
	}
}
