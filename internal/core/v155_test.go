package core

import (
	"context"
	"io"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// fakeSocksServer stands in for Tor's SOCKS port: it answers SOCKS5
// CONNECT requests with replyCode (0x00 = reachable). silent=true makes
// it accept and never answer, so client probes hit their deadline.
func fakeSocksServer(t *testing.T, replyCode byte, silent bool) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				greet := make([]byte, 2)
				if _, err := io.ReadFull(c, greet); err != nil {
					return
				}
				if _, err := c.Write([]byte{0x05, 0x00}); err != nil {
					return
				}
				head := make([]byte, 5) // ver, cmd, rsv, atyp, host length
				if _, err := io.ReadFull(c, head); err != nil {
					return
				}
				switch head[3] {
				case 0x03:
					rest := make([]byte, int(head[4])+2)
					if _, err := io.ReadFull(c, rest); err != nil {
						return
					}
				case 0x01:
					rest := make([]byte, 4+2)
					if _, err := io.ReadFull(c, rest); err != nil {
						return
					}
				}
				if silent {
					// never answer: the probe's own deadline must fire
					time.Sleep(30 * time.Second)
					return
				}
				// ver, rep, rsv, atyp=IPv4, 127.0.0.1, port 0
				_, _ = c.Write([]byte{0x05, replyCode, 0x00, 0x01, 127, 0, 0, 1, 0x00, 0x00})
			}(conn)
		}
	}()
	return ln.Addr().(*net.TCPAddr).Port
}

// TestProbeOnionsRound pins the v1.5.5 contract of the Tor panel's
// reachability check: ONE parallel round, every probe bounded by the
// per-probe deadline, reachable onions reported as live.
func TestProbeOnionsRound(t *testing.T) {
	eng, _, st := newTestEngine(t)
	if err := st.UpdateServer(KindSMP, func(r *ServerRec) { r.OnionHost = "aaaaaaaa.onion"; r.Port = 5223 }); err != nil {
		t.Fatal(err)
	}
	if err := st.UpdateServer(KindXFTP, func(r *ServerRec) { r.OnionHost = "bbbbbbbb.onion"; r.Port = 0 }); err != nil {
		t.Fatal(err)
	}
	up := fakeSocksServer(t, 0x00, false)      // replies success
	refused := fakeSocksServer(t, 0x04, false) // replies "host unreachable"
	silent := fakeSocksServer(t, 0x00, true)   // never replies
	cases := []struct {
		name string
		port int
		want []Kind
	}{
		{"all reachable", up, []Kind{KindSMP, KindXFTP}},
		{"unreachable replies", refused, nil},
		{"silent server still bounded", silent, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := st.With(func(s *State) { s.Tor.SocksPort = tc.port }); err != nil {
				t.Fatal(err)
			}
			begin := time.Now()
			live := eng.probeOnionsRound([]Kind{KindSMP, KindXFTP}, 300*time.Millisecond)
			if elapsed := time.Since(begin); elapsed > 3*time.Second {
				t.Fatalf("round took %s - probes must be bounded by perProbe", elapsed)
			}
			if len(live) != len(tc.want) {
				t.Fatalf("live = %v, want %v", live, tc.want)
			}
			for _, k := range tc.want {
				if !kindIn(k, live) {
					t.Fatalf("live = %v, want %v (missing %v)", live, tc.want, k)
				}
			}
		})
	}
}

// TestWaitForOnionsBudget pins that the onion-hostname wait honors the
// caller's budget (v1.5.4 hard-wired 4 minutes for every caller - the
// Tor panel's Connect blocked that long after Tor was already up).
func TestWaitForOnionsBudget(t *testing.T) {
	eng, paths, st := newTestEngine(t)
	eng.Backend = &nativeBackend{eng: eng}
	if err := st.UpdateServer(KindSMP, func(r *ServerRec) { r.Installed = true }); err != nil {
		t.Fatal(err)
	}
	// no hostname file: a 1ns budget must time out immediately
	begin := time.Now()
	if err := eng.waitForOnions(context.Background(), []Kind{KindSMP}, time.Nanosecond); err == nil {
		t.Fatal("expected a timeout error")
	}
	if elapsed := time.Since(begin); elapsed > 2*time.Second {
		t.Fatalf("budget not honored: %s", elapsed)
	}
	// with the hostname file present the wait returns on the first poll
	dir := paths.HiddenServiceDir(KindSMP)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "hostname"), []byte("aaaaaaaa.onion\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := eng.waitForOnions(context.Background(), []Kind{KindSMP}, 4*time.Minute); err != nil {
		t.Fatalf("hostname present but wait failed: %v", err)
	}
}
