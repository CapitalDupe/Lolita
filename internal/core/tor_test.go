package core

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// newTestTorManager builds a TorManager over a temp directory.
func newTestTorManager(t *testing.T) (*TorManager, *Paths, *StateStore) {
	t.Helper()
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
	return NewTorManager(paths, state, NopInstallLog()), paths, state
}

// freePort finds a currently-free loopback port.
func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

// pinned ports that are still free must be reused as-is.
func TestGenerateTorrcKeepsFreePinnedPorts(t *testing.T) {
	tm, paths, state := newTestTorManager(t)
	socks, ctrl := freePort(t), freePort(t)
	if err := state.With(func(st *State) {
		st.Tor.SocksPort = socks
		st.Tor.ControlPort = ctrl
	}); err != nil {
		t.Fatal(err)
	}
	if err := tm.GenerateTorrc(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(paths.TorDir(), "torrc"))
	if err != nil {
		t.Fatal(err)
	}
	torrc := string(data)
	if !strings.Contains(torrc, "SocksPort 127.0.0.1:"+strconv.Itoa(socks)+"\n") {
		t.Errorf("torrc should keep the free pinned socks port %d:\n%s", socks, torrc)
	}
	if !strings.Contains(torrc, "ControlPort 127.0.0.1:"+strconv.Itoa(ctrl)+"\n") {
		t.Errorf("torrc should keep the free pinned control port %d:\n%s", ctrl, torrc)
	}
}

// pinned ports stolen by another listener (typically the distro Tor
// service started by installing the "tor" package) must be re-picked,
// persisted and used - the previous behaviour kept writing them into
// torrc, so Tor died on "address already in use" with no connection.
func TestGenerateTorrcRePicksOccupiedPorts(t *testing.T) {
	tm, paths, state := newTestTorManager(t)
	// occupy both pinned ports with foreign listeners
	l1, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l1.Close()
	l2, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l2.Close()
	socks := l1.Addr().(*net.TCPAddr).Port
	ctrl := l2.Addr().(*net.TCPAddr).Port
	if err := state.With(func(st *State) {
		st.Tor.SocksPort = socks
		st.Tor.ControlPort = ctrl
	}); err != nil {
		t.Fatal(err)
	}
	if err := tm.GenerateTorrc(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(paths.TorDir(), "torrc"))
	if err != nil {
		t.Fatal(err)
	}
	torrc := string(data)
	if strings.Contains(torrc, fmt.Sprintf(":%d", socks)) || strings.Contains(torrc, fmt.Sprintf(":%d", ctrl)) {
		t.Errorf("torrc must not reuse the occupied ports %d/%d:\n%s", socks, ctrl, torrc)
	}
	got := state.Get().Tor
	if got.SocksPort == socks || got.ControlPort == ctrl || got.SocksPort == 0 || got.ControlPort == 0 {
		t.Errorf("state should pin fresh ports, got %d/%d (was %d/%d)", got.SocksPort, got.ControlPort, socks, ctrl)
	}
	if !strings.Contains(torrc, "SocksPort 127.0.0.1:"+strconv.Itoa(got.SocksPort)+"\n") {
		t.Errorf("torrc should use the re-picked socks port %d:\n%s", got.SocksPort, torrc)
	}
	if !tcpPortFree(got.SocksPort) || !tcpPortFree(got.ControlPort) {
		t.Errorf("re-picked ports %d/%d must be free", got.SocksPort, got.ControlPort)
	}
}

// fakeTorScript writes a python "tor" binary that speaks the parts of the
// real control-port protocol the TorManager uses, with tor's real wire
// format: "250-AUTH METHODS=COOKIE COOKIEFILE=..." and
// "BOOTSTRAP PROGRESS=100 TAG=Done SUMMARY=...". mode "die" appends an
// error to the notice log and exits immediately.
const fakeTor = `#!/usr/bin/env python3
import os, sys, socket, signal, threading

cfg = {}
for line in open(sys.argv[sys.argv.index('-f') + 1]):
    line = line.strip()
    if not line or line.startswith('#'):
        continue
    parts = line.split(None, 1)
    if len(parts) == 2:
        cfg[parts[0]] = parts[1]

mode = os.environ.get('FAKE_TOR_MODE', 'ok')
logline = cfg.get('Log', '')
logpath = logline.split()[-1] if logline else ''

if mode == 'die':
    with open(logpath, 'a') as f:
        f.write('Sep 01 00:00:00.000 [warn] Could not bind 127.0.0.1: '
                'Address already in use. Is Tor already running?\n')
    sys.exit(1)

pidfile = cfg['PidFile']
ctrl = int(cfg['ControlPort'].rsplit(':', 1)[1])
cookie = os.path.join(os.path.dirname(pidfile), 'control.authcookie')
with open(cookie, 'wb') as f:
    f.write(b'\x00' * 32)
with open(pidfile, 'w') as f:
    f.write(str(os.getpid()))

def handle(c):
    f = c.makefile('rwb')
    for line in f:
        u = line.strip().upper()
        if u.startswith(b'PROTOCOLINFO'):
            f.write(b'250-PROTOCOLINFO 1\r\n')
            f.write(b'250-AUTH METHODS=COOKIE COOKIEFILE="' + cookie.encode() + b'"\r\n')
            f.write(b'250 OK\r\n')
            f.flush()
        elif u.startswith(b'AUTHENTICATE'):
            f.write(b'250 OK\r\n')
            f.flush()
        elif u.startswith(b'GETINFO'):
            f.write(b'250-status/bootstrap-phase=BOOTSTRAP PROGRESS=100 TAG=Done SUMMARY="Done"\r\n')
            f.write(b'250 OK\r\n')
            f.flush()
        elif u.startswith(b'SIGNAL'):
            f.write(b'250 OK\r\n')
            f.flush()
            os._exit(0)
        elif u.startswith(b'QUIT'):
            f.write(b'250 closing connection\r\n')
            f.flush()
            break
    c.close()

signal.signal(signal.SIGINT, lambda *a: os._exit(0))
signal.signal(signal.SIGTERM, lambda *a: os._exit(0))
srv = socket.socket()
srv.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
srv.bind(('127.0.0.1', ctrl))
srv.listen(8)
while True:
    try:
        c, _ = srv.accept()
    except OSError:
        break
    threading.Thread(target=handle, args=(c,), daemon=True).start()
`

// writeFakeTor installs the fake tor binary where FindTor looks first.
func writeFakeTor(t *testing.T, tm *TorManager) {
	t.Helper()
	p := tm.Paths.TorBinPath()
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(fakeTor), 0o755); err != nil {
		t.Fatal(err)
	}
}

// TestStartWithFakeTor covers the full Start -> bootstrap -> restart
// (bounce) -> Stop cycle against a control port that answers exactly
// like the real tor - this is the path that was broken by the wrong
// COOKIEFILE/PROTOCOLINFO parsing (sxops could never see Tor as
// connected, so every retry ended in "did not connect within 5
// minutes" even though Tor was fine).
func TestStartWithFakeTor(t *testing.T) {
	if _, err := execLookPython(); err != nil {
		t.Skip("python3 not available for the fake tor")
	}
	tm, _, state := newTestTorManager(t)
	writeFakeTor(t, tm)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := tm.Start(ctx); err != nil {
		t.Fatalf("first Start: %v", err)
	}
	if !state.Get().Tor.OnionReady {
		t.Fatal("OnionReady should be true after bootstrap")
	}
	if phase, ok := tm.BootstrapStatus(); !ok || !strings.Contains(phase, "100") {
		t.Fatalf("BootstrapStatus after start: %q ok=%v", phase, ok)
	}
	if !tm.Running() {
		t.Fatal("Running() should be true")
	}

	// restart with new config (bounce) must not deadlock and must
	// bootstrap again on the same ports
	if err := tm.Start(ctx); err != nil {
		t.Fatalf("second Start (bounce): %v", err)
	}
	if phase, ok := tm.BootstrapStatus(); !ok || !strings.Contains(phase, "100") {
		t.Fatalf("BootstrapStatus after bounce: %q ok=%v", phase, ok)
	}

	if err := tm.Stop(); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if tm.Running() {
		t.Fatal("Running() should be false after Stop")
	}
}

// TestStartFastFailWhenTorDies verifies that a tor that exits instantly
// (e.g. a port conflict) is reported quickly with the log tail, instead
// of silently burning the whole 5 minute bootstrap window.
func TestStartFastFailWhenTorDies(t *testing.T) {
	if _, err := execLookPython(); err != nil {
		t.Skip("python3 not available for the fake tor")
	}
	tm, _, _ := newTestTorManager(t)
	writeFakeTor(t, tm)
	t.Setenv("FAKE_TOR_MODE", "die")

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	start := time.Now()
	err := tm.Start(ctx)
	if err == nil {
		tm.Stop()
		t.Fatal("Start should fail when tor dies immediately")
	}
	if el := time.Since(start); el > 45*time.Second {
		tm.Stop()
		t.Fatalf("fast-fail took %v, expected well under 45s", el)
	}
	if !strings.Contains(err.Error(), "Tor exited") {
		t.Fatalf("error should say Tor exited, got: %v", err)
	}
	if !strings.Contains(err.Error(), "Address already in use") {
		t.Fatalf("error should include the tor log tail, got: %v", err)
	}
	tm.Stop()
}

func execLookPython() (string, error) {
	return exec.LookPath("python3")
}
