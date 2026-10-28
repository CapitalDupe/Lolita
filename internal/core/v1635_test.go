package core

// v1.6.35/v1.6.37 tests: the bridge connection failure counter and
// the waitBootstrap bridge-stall diagnosis (with a fake Tor control
// port). The v1.6.35 TCP preflight (PreflightBridges + bridgeAddr) was
// removed in v1.6.37 at the operator's request, and so were its tests.

import (
        "context"
        "fmt"
        "net"
        "os"
        "path/filepath"
        "strings"
        "testing"
        "time"
)

func TestBridgeFailureCount(t *testing.T) {
        var b strings.Builder
        for i := 0; i < 3; i++ {
                b.WriteString("Oct 26 17:55:15.000 [warn] Proxy Client: unable to connect OR connection (handshaking (proxy)) with [scrubbed] (\"general SOCKS server failure\")\n")
        }
        b.WriteString("Oct 26 17:56:02.000 [warn] Connect to 203.0.113.9:443 failed: Connection refused\n")
        b.WriteString("Oct 26 17:56:02.000 [notice] New control connection opened from 127.0.0.1.\n")
        if n := bridgeFailureCount(b.String()); n != 4 {
                t.Fatalf("bridgeFailureCount = %d, want 4", n)
        }
        if n := bridgeFailureCount(""); n != 0 {
                t.Fatalf("bridgeFailureCount(\"\") = %d, want 0", n)
        }
}

// fakeTorControlPort serves just enough of the Tor control protocol for
// BootstrapStatus: PROTOCOLINFO (cookie file), AUTHENTICATE and a fixed
// bootstrap-phase reply.
func fakeTorControlPort(t *testing.T, cookieFile, progress string) int {
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
                        go func(conn net.Conn) {
                                defer conn.Close()
                                buf := make([]byte, 4096)
                                if _, err := conn.Read(buf); err != nil {
                                        return
                                }
                                fmt.Fprintf(conn, "250-PROTOCOLINFO 1 AUTH METHODS=COOKIE COOKIEFILE=%q\r\n250 OK\r\n", cookieFile)
                                if _, err := conn.Read(buf); err != nil {
                                        return
                                }
                                fmt.Fprint(conn, "250 OK\r\n")
                                if _, err := conn.Read(buf); err != nil {
                                        return
                                }
                                fmt.Fprintf(conn, "250-status/bootstrap-phase=BOOTSTRAP PROGRESS=%s TAG=status SUMMARY=\"Asking for networkstatus consensus\"\r\n250 OK\r\n", progress)
                        }(conn)
                }
        }()
        return ln.Addr().(*net.TCPAddr).Port
}

// shortDiagnosis makes the fail-fast verdict fire in seconds instead of
// the production two minutes.
func shortDiagnosis(t *testing.T) {
        t.Helper()
        oldA, oldAfter := bridgeFailFastAttempts, bridgeFailFastAfter
        bridgeFailFastAttempts, bridgeFailFastAfter = 2, 2*time.Second
        t.Cleanup(func() {
                bridgeFailFastAttempts, bridgeFailFastAfter = oldA, oldAfter
        })
}

// seedTorLog writes the given content as the Tor notice log.
func seedTorLog(t *testing.T, paths *Paths, content string) string {
        t.Helper()
        if err := os.MkdirAll(filepath.Dir(paths.TorLogFile()), 0o700); err != nil {
                t.Fatal(err)
        }
        if err := os.WriteFile(paths.TorLogFile(), []byte(content), 0o600); err != nil {
                t.Fatal(err)
        }
        return paths.TorLogFile()
}

func deadBridgeLog(n int) string {
        var b strings.Builder
        for i := 0; i < n; i++ {
                b.WriteString(fmt.Sprintf("Oct 26 17:55:%02d.000 [warn] Proxy Client: unable to connect OR connection (handshaking (proxy)) with [scrubbed] (\"general SOCKS server failure\")\n", 15+i))
        }
        return b.String()
}

// TestWaitBootstrapBridgeFailFast pins the v1.6.35 behavior: with bridges
// configured, a bootstrap stuck at 25% plus repeated bridge connection
// failures in the log is aborted early with the diagnosis instead of
// the silent five-minute stall.
func TestWaitBootstrapBridgeFailFast(t *testing.T) {
        dir := t.TempDir()
        paths, err := NewPaths(dir)
        if err != nil {
                t.Fatal(err)
        }
        if err := paths.Ensure(); err != nil {
                t.Fatal(err)
        }
        state, err := NewStateStore(paths)
        if err != nil {
                t.Fatal(err)
        }
        s := state.Settings()
        s.TorBridges = []string{"obfs4 203.0.113.9:443 cert=abc iat-mode=0"}
        if err := state.SetSettings(s); err != nil {
                t.Fatal(err)
        }
        cookieFile := filepath.Join(dir, "cookie")
        if err := os.WriteFile(cookieFile, make([]byte, 32), 0o600); err != nil {
                t.Fatal(err)
        }
        ctrl := fakeTorControlPort(t, cookieFile, "25")
        _ = state.With(func(st *State) {
                st.Tor.ControlPort = ctrl
                st.Tor.SocksPort = 9050
        })
        seedTorLog(t, paths, deadBridgeLog(6))
        shortDiagnosis(t)

        tm := NewTorManager(paths, state, NopInstallLog())
        ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
        defer cancel()
        started := time.Now()
        err = tm.waitBootstrap(ctx)
        if err == nil {
                t.Fatal("expected the bridge diagnosis error")
        }
        if !strings.Contains(err.Error(), "bridges are not connecting") {
                t.Fatalf("error is not the bridge diagnosis:\n%s", err)
        }
        if !strings.Contains(err.Error(), "tried them 6 times") {
                t.Fatalf("diagnosis does not report the attempt count:\n%s", err)
        }
        if elapsed := time.Since(started); elapsed > 15*time.Second {
                t.Fatalf("diagnosis took %s - fail-fast is not fast", elapsed)
        }
        if !state.Get().Tor.ConnectFailed {
                t.Error("ConnectFailed was not recorded in the state")
        }
}

// TestWaitBootstrapNoFailFastWhenBootstrapped guards against a false
// positive: a bootstrap that reached 100% succeeds even when old bridge
// failures sit in the log.
func TestWaitBootstrapNoFailFastWhenBootstrapped(t *testing.T) {
        dir := t.TempDir()
        paths, _ := NewPaths(dir)
        if err := paths.Ensure(); err != nil {
                t.Fatal(err)
        }
        state, err := NewStateStore(paths)
        if err != nil {
                t.Fatal(err)
        }
        s := state.Settings()
        s.TorBridges = []string{"203.0.113.9:443 0099AABBCCDD"}
        if err := state.SetSettings(s); err != nil {
                t.Fatal(err)
        }
        cookieFile := filepath.Join(dir, "cookie")
        if err := os.WriteFile(cookieFile, make([]byte, 32), 0o600); err != nil {
                t.Fatal(err)
        }
        ctrl := fakeTorControlPort(t, cookieFile, "100")
        _ = state.With(func(st *State) {
                st.Tor.ControlPort = ctrl
                st.Tor.SocksPort = 9050
        })
        seedTorLog(t, paths, deadBridgeLog(6))
        shortDiagnosis(t)

        tm := NewTorManager(paths, state, NopInstallLog())
        ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
        defer cancel()
        if err := tm.waitBootstrap(ctx); err != nil {
                t.Fatalf("bootstrapped Tor must not fail: %v", err)
        }
}

// TestWaitBootstrapNoFailFastWhenProgressing guards the other false
// positive: a bootstrap that got PAST 25% (the bridges do connect, only
// the directory fetch is slow) must not be aborted by the old failures
// in the log.
func TestWaitBootstrapNoFailFastWhenProgressing(t *testing.T) {
        dir := t.TempDir()
        paths, _ := NewPaths(dir)
        if err := paths.Ensure(); err != nil {
                t.Fatal(err)
        }
        state, err := NewStateStore(paths)
        if err != nil {
                t.Fatal(err)
        }
        s := state.Settings()
        s.TorBridges = []string{"203.0.113.9:443 0099AABBCCDD"}
        if err := state.SetSettings(s); err != nil {
                t.Fatal(err)
        }
        cookieFile := filepath.Join(dir, "cookie")
        if err := os.WriteFile(cookieFile, make([]byte, 32), 0o600); err != nil {
                t.Fatal(err)
        }
        ctrl := fakeTorControlPort(t, cookieFile, "50")
        _ = state.With(func(st *State) {
                st.Tor.ControlPort = ctrl
                st.Tor.SocksPort = 9050
        })
        seedTorLog(t, paths, deadBridgeLog(6))
        shortDiagnosis(t)

        tm := NewTorManager(paths, state, NopInstallLog())
        ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
        defer cancel()
        err = tm.waitBootstrap(ctx)
        if err == nil {
                t.Fatal("expected the context timeout, not success")
        }
        if strings.Contains(err.Error(), "bridges are not connecting") {
                t.Fatalf("fail-fast fired although progress passed 25%%:\n%s", err)
        }
}
