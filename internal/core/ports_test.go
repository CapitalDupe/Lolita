package core

import (
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// newTestEngine builds a full Engine over a temp directory (no processes
// are started - it is only used for the port logic).
func newTestEngine(t *testing.T) (*Engine, *Paths, *StateStore) {
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
	eng := &Engine{
		Paths: paths,
		State: state,
		Sv:    NewSupervisor(paths, state),
		Tor:   NewTorManager(paths, state, NopInstallLog()),
		Log:   NopInstallLog(),
	}
	return eng, paths, state
}

// testPorts is a set of three currently-free ports used by the fake INI so
// the tests never collide with a real sxops deployment running on the
// canonical ports (5223/5224/8023) of the same machine.
type testPorts struct {
	transport, control, web int
}

func freeTestPorts(t *testing.T) testPorts {
	t.Helper()
	return testPorts{transport: freePort(t), control: freePort(t), web: freePort(t)}
}

// fakeInstalledSMP writes an smp-server.ini resembling the real one after
// init (transport + control + web ports at the given values) and marks the
// kind installed in state.
func fakeInstalledSMPAt(t *testing.T, eng *Engine, p testPorts) {
	t.Helper()
	cfgDir := eng.Paths.CfgDir(KindSMP)
	if err := os.MkdirAll(cfgDir, 0o700); err != nil {
		t.Fatal(err)
	}
	ini := `[TRANSPORT]
ip = 127.0.0.1
port = ` + strconv.Itoa(p.transport) + `
control_port = ` + strconv.Itoa(p.control) + `
host =

[WEB]
http = ` + strconv.Itoa(p.web) + `

[AUTH]
control_port_admin_password = abc
control_port_user_password = def
`
	if err := os.WriteFile(filepath.Join(cfgDir, "smp-server.ini"), []byte(ini), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := eng.State.UpdateServer(KindSMP, func(r *ServerRec) {
		r.Installed = true
		r.Port = p.transport
		r.ControlPort = p.control
		r.WebPort = p.web
	}); err != nil {
		t.Fatal(err)
	}
}

// iniValue reads one value back from the written INI.
func iniValue(t *testing.T, eng *Engine, k Kind, section, key string) string {
	t.Helper()
	d := Def(k)
	ini, err := LoadIni(filepath.Join(eng.Paths.CfgDir(k), d.IniName))
	if err != nil {
		t.Fatal(err)
	}
	v, _ := ini.Get(section, key)
	return v
}

// A taken transport port must move the LOCAL port (INI + state), while
// the advertised port - used in the address and the onion virtual port -
// stays unchanged so client addresses keep working.
func TestEnsureServerPortsMovesTakenTransportPort(t *testing.T) {
	eng, _, state := newTestEngine(t)
	p := freeTestPorts(t)
	fakeInstalledSMPAt(t, eng, p)

	// a foreign listener holds the transport port
	l, err := net.Listen("tcp", "127.0.0.1:"+strconv.Itoa(p.transport))
	if err != nil {
		t.Skip("transport port not bindable in this environment")
	}
	defer l.Close()

	moved, err := eng.EnsureServerPorts(KindSMP)
	if err != nil {
		t.Fatal(err)
	}
	if len(moved) != 1 || !strings.Contains(moved[0], strconv.Itoa(p.transport)) || !strings.Contains(moved[0], "instead") {
		t.Fatalf("expected one transport move message, got %v", moved)
	}
	rec := state.Server(KindSMP)
	if rec.Port != p.transport {
		t.Errorf("advertised port must stay %d, got %d", p.transport, rec.Port)
	}
	if rec.LocalPort == 0 || rec.LocalPort == p.transport {
		t.Errorf("local port must move off %d, got %d", p.transport, rec.LocalPort)
	}
	if got := iniValue(t, eng, KindSMP, "TRANSPORT", "port"); got != strconv.Itoa(rec.LocalPort) {
		t.Errorf("INI transport port = %s, state LocalPort = %d (must match)", got, rec.LocalPort)
	}
	// control + web untouched (their ports were free)
	if got := iniValue(t, eng, KindSMP, "TRANSPORT", "control_port"); got != strconv.Itoa(p.control) {
		t.Errorf("control port must stay %d, got %s", p.control, got)
	}
	if got := iniValue(t, eng, KindSMP, "WEB", "http"); got != strconv.Itoa(p.web) {
		t.Errorf("web port must stay %d, got %s", p.web, got)
	}
	// the new local port is actually bindable now
	if !tcpPortFree(rec.LocalPort) {
		t.Errorf("chosen local port %d is not free", rec.LocalPort)
	}
}

// Free ports are left alone (idempotent no-op).
func TestEnsureServerPortsNoMoveWhenFree(t *testing.T) {
	eng, _, state := newTestEngine(t)
	p := freeTestPorts(t)
	fakeInstalledSMPAt(t, eng, p)

	moved, err := eng.EnsureServerPorts(KindSMP)
	if err != nil {
		t.Fatal(err)
	}
	if len(moved) != 0 {
		t.Fatalf("expected no moves, got %v", moved)
	}
	rec := state.Server(KindSMP)
	if rec.LocalPort != 0 {
		t.Errorf("LocalPort must stay 0 (= canonical), got %d", rec.LocalPort)
	}
	if got := iniValue(t, eng, KindSMP, "TRANSPORT", "port"); got != strconv.Itoa(p.transport) {
		t.Errorf("INI port must stay %d, got %s", p.transport, got)
	}
}

// Taken control and web ports move too, without touching the transport.
func TestEnsureServerPortsMovesControlAndWeb(t *testing.T) {
	eng, _, state := newTestEngine(t)
	p := freeTestPorts(t)
	fakeInstalledSMPAt(t, eng, p)

	lc, err := net.Listen("tcp", "127.0.0.1:"+strconv.Itoa(p.control))
	if err != nil {
		t.Skip("control port not bindable in this environment")
	}
	defer lc.Close()
	lw, err := net.Listen("tcp", "127.0.0.1:"+strconv.Itoa(p.web))
	if err != nil {
		t.Skip("web port not bindable in this environment")
	}
	defer lw.Close()

	moved, err := eng.EnsureServerPorts(KindSMP)
	if err != nil {
		t.Fatal(err)
	}
	if len(moved) != 2 {
		t.Fatalf("expected control+web moves, got %v", moved)
	}
	rec := state.Server(KindSMP)
	if rec.LocalPort != 0 || iniValue(t, eng, KindSMP, "TRANSPORT", "port") != strconv.Itoa(p.transport) {
		t.Errorf("transport port must stay %d (it was free), LocalPort=%d ini=%s", p.transport, rec.LocalPort, iniValue(t, eng, KindSMP, "TRANSPORT", "port"))
	}
	if rec.ControlPort == p.control || rec.ControlPort == 0 {
		t.Errorf("control port must move off %d, got %d", p.control, rec.ControlPort)
	}
	if got := iniValue(t, eng, KindSMP, "TRANSPORT", "control_port"); got != strconv.Itoa(rec.ControlPort) {
		t.Errorf("INI control port %s != state %d", got, rec.ControlPort)
	}
	if rec.WebPort == p.web || rec.WebPort == 0 {
		t.Errorf("web port must move off %d, got %d", p.web, rec.WebPort)
	}
	if got := iniValue(t, eng, KindSMP, "WEB", "http"); got != strconv.Itoa(rec.WebPort) {
		t.Errorf("INI web port %s != state %d", got, rec.WebPort)
	}
}

// A second call is a no-op once everything sits on free ports.
func TestEnsureServerPortsIdempotent(t *testing.T) {
	eng, _, _ := newTestEngine(t)
	p := freeTestPorts(t)
	fakeInstalledSMPAt(t, eng, p)

	l, err := net.Listen("tcp", "127.0.0.1:"+strconv.Itoa(p.transport))
	if err != nil {
		t.Skip("transport port not bindable in this environment")
	}
	defer l.Close()

	if _, err := eng.EnsureServerPorts(KindSMP); err != nil {
		t.Fatal(err)
	}
	moved, err := eng.EnsureServerPorts(KindSMP)
	if err != nil {
		t.Fatal(err)
	}
	if len(moved) != 0 {
		t.Fatalf("second call must not move anything, got %v", moved)
	}
}

// The torrc maps the VIRTUAL (advertised) port to the LOCAL port the
// server actually binds after a conflict move.
func TestGenerateTorrcVirtualToLocalMapping(t *testing.T) {
	eng, paths, _ := newTestEngine(t)
	p := freeTestPorts(t)
	fakeInstalledSMPAt(t, eng, p)

	// simulate a previous move: local port -> transport+10
	local := p.transport + 10
	if err := eng.State.UpdateServer(KindSMP, func(r *ServerRec) { r.LocalPort = local }); err != nil {
		t.Fatal(err)
	}
	if err := eng.Tor.GenerateTorrc(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(paths.TorDir(), "torrc"))
	if err != nil {
		t.Fatal(err)
	}
	torrc := string(data)
	if !strings.Contains(torrc, "HiddenServicePort "+strconv.Itoa(p.transport)+" 127.0.0.1:"+strconv.Itoa(local)) {
		t.Errorf("torrc must map virtual %d to local %d:\n%s", p.transport, local, torrc)
	}
	// web info page follows the (possibly moved) web port
	if !strings.Contains(torrc, "HiddenServicePort 80 127.0.0.1:"+strconv.Itoa(p.web)) {
		t.Errorf("torrc must keep the info page at virtual 80 -> %d:\n%s", p.web, torrc)
	}
}

// pickFreePortNear must skip ports in use or reserved for other
// components and land on a bindable port.
func TestPickFreePortNearSkipsUsedAndReserved(t *testing.T) {
	base := freePort(t)
	// a real listener on base+1: the scan must skip it
	l1, err := net.Listen("tcp", "127.0.0.1:"+strconv.Itoa(base+1))
	if err != nil {
		t.Skip("cannot bind base+1 in this environment")
	}
	defer l1.Close()
	got := pickFreePortNear(base, map[int]bool{})
	if got <= base+1 {
		t.Fatalf("expected a port after the held port %d, got %d", base+1, got)
	}
	if !tcpPortFree(got) {
		t.Fatalf("returned port %d is not free", got)
	}
	// explicit exclusions are honored too
	excl := map[int]bool{base + 1: true, base + 2: true, base + 3: true}
	got2 := pickFreePortNear(base, excl)
	if got2 <= base+3 {
		t.Fatalf("expected the scan to skip excluded ports, got %d", got2)
	}
}

// The transport port must never move ONTO the server's own control or
// web port (two listeners cannot share one port - the server would die
// at startup). Regression test for the SMP 5223->5224 collision.
func TestEnsureServerPortsSkipsOwnControlPort(t *testing.T) {
	eng, _, state := newTestEngine(t)
	// transport and control adjacent: transport = P, control = P+1
	p := freePort(t)
	ctrl := p + 1
	if !tcpPortFree(ctrl) {
		t.Skip("adjacent control port not free in this environment")
	}
	web := freePort(t)
	fakeInstalledSMPAt(t, eng, testPorts{transport: p, control: ctrl, web: web})

	// a foreign listener holds the transport port
	l, err := net.Listen("tcp", "127.0.0.1:"+strconv.Itoa(p))
	if err != nil {
		t.Skip("transport port not bindable in this environment")
	}
	defer l.Close()

	moved, err := eng.EnsureServerPorts(KindSMP)
	if err != nil {
		t.Fatal(err)
	}
	if len(moved) == 0 {
		t.Fatal("expected the transport port to move")
	}
	rec := state.Server(KindSMP)
	if rec.LocalPort == p {
		t.Fatalf("transport port did not move: %d", rec.LocalPort)
	}
	if rec.LocalPort == ctrl {
		t.Fatalf("transport port moved onto the server's own control port %d", ctrl)
	}
	if rec.LocalPort == web {
		t.Fatalf("transport port moved onto the server's own web port %d", web)
	}
	// control port stays (it was free)
	if rec.ControlPort != ctrl {
		t.Errorf("control port must stay %d, got %d", ctrl, rec.ControlPort)
	}
}
