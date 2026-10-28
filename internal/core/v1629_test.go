package core

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// v1.6.29: the SMP server's embedded HTTPS web page. Short links
// (https://<server>/a#key) carry no port, so a browser connects to port
// 443 - sxops maps the onion service's virtual port 443 onto the local
// HTTPS web port (8443, movable by the port-conflict resolution) and
// maintains the self-signed certificate the server serves it with.
// Opening a link in Tor Browser then shows the official SimpleX invite
// page (with the QR code) instead of "connection refused".

// newWebTestEngine builds an engine with the native backend over a temp
// dir and a v1.6.28-style installed SMP server (INI without the https
// keys, no certificate).
func newWebTestEngine(t *testing.T) (*Engine, *Paths) {
	t.Helper()
	eng, paths, _ := newTestEngine(t)
	eng.Backend = &nativeBackend{eng: eng}
	p := freeTestPorts(t)
	fakeInstalledSMPAt(t, eng, p)
	if err := os.WriteFile(filepath.Join(paths.CfgDir(KindSMP), "fingerprint"), []byte("FPFPFP\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return eng, paths
}

func TestTorrcMapsVirtual443ToSMPWebPage(t *testing.T) {
	eng, paths, _ := newTestEngine(t)
	p := freeTestPorts(t)
	fakeInstalledSMPAt(t, eng, p)
	if err := eng.Tor.GenerateTorrc(); err != nil {
		t.Fatal(err)
	}
	torrc := readFileT(t, filepath.Join(paths.TorDir(), "torrc"))
	if !strings.Contains(torrc, "HiddenServicePort 443 127.0.0.1:8443") {
		t.Errorf("torrc must map virtual 443 to the default HTTPS web port:\n%s", torrc)
	}
	if !eng.Tor.torrcHasDefaultPorts() {
		t.Errorf("torrcHasDefaultPorts must accept the fresh layout:\n%s", torrc)
	}

	// a port the conflict resolution moved is the local target
	if err := eng.State.UpdateServer(KindSMP, func(r *ServerRec) { r.WebHttpsPort = 8450 }); err != nil {
		t.Fatal(err)
	}
	if err := eng.Tor.GenerateTorrc(); err != nil {
		t.Fatal(err)
	}
	torrc = readFileT(t, filepath.Join(paths.TorDir(), "torrc"))
	if !strings.Contains(torrc, "HiddenServicePort 443 127.0.0.1:8450") {
		t.Errorf("torrc must follow the moved HTTPS web port:\n%s", torrc)
	}

	// info pages off: no mapping, and the detector does not demand one
	if err := eng.State.With(func(st *State) { st.Settings.EnableInfoPages = false }); err != nil {
		t.Fatal(err)
	}
	if err := eng.Tor.GenerateTorrc(); err != nil {
		t.Fatal(err)
	}
	torrc = readFileT(t, filepath.Join(paths.TorDir(), "torrc"))
	if strings.Contains(torrc, "HiddenServicePort 443") {
		t.Errorf("torrc must not map virtual 443 with info pages off:\n%s", torrc)
	}
	if !eng.Tor.torrcHasDefaultPorts() {
		t.Errorf("torrcHasDefaultPorts must not require the mapping with info pages off:\n%s", torrc)
	}
}

func TestTorrcWeb443CheckScopedToSMPBlock(t *testing.T) {
	eng, paths, _ := newTestEngine(t)
	p := freeTestPorts(t)
	fakeInstalledSMPAt(t, eng, p)
	// XFTP installed too - its block carries its own 443 line (the XFTP
	// protocol default port), which must NOT mask a missing SMP mapping.
	if err := eng.State.UpdateServer(KindXFTP, func(r *ServerRec) {
		r.Installed = true
		r.Port = 5225
		r.LocalPort = 5225
	}); err != nil {
		t.Fatal(err)
	}
	torrcPath := filepath.Join(paths.TorDir(), "torrc")
	old := "# SMP server onion service\n" +
		"HiddenServiceDir " + paths.HiddenServiceDir(KindSMP) + "\n" +
		"HiddenServicePort " + strconv.Itoa(p.transport) + " 127.0.0.1:" + strconv.Itoa(p.transport) + "\n" +
		"HiddenServicePort 5223 127.0.0.1:" + strconv.Itoa(p.transport) + "\n" +
		"HiddenServicePort 80 127.0.0.1:" + strconv.Itoa(p.web) + "\n\n" +
		"# XFTP server onion service\n" +
		"HiddenServiceDir " + paths.HiddenServiceDir(KindXFTP) + "\n" +
		"HiddenServicePort 5225 127.0.0.1:5225\n" +
		"HiddenServicePort 443 127.0.0.1:5225\n" +
		"HiddenServicePort 80 127.0.0.1:8025\n"
	if err := os.WriteFile(torrcPath, []byte(old), 0o600); err != nil {
		t.Fatal(err)
	}
	if eng.Tor.torrcHasDefaultPorts() {
		t.Error("a missing SMP virtual-443 mapping must be detected even when the XFTP block has a 443 line")
	}
	// adding the SMP mapping makes the layout current
	fixed := strings.Replace(old,
		"HiddenServicePort 80 127.0.0.1:"+strconv.Itoa(p.web)+"\n\n",
		"HiddenServicePort 80 127.0.0.1:"+strconv.Itoa(p.web)+"\nHiddenServicePort 443 127.0.0.1:8443\n\n", 1)
	if err := os.WriteFile(torrcPath, []byte(fixed), 0o600); err != nil {
		t.Fatal(err)
	}
	if !eng.Tor.torrcHasDefaultPorts() {
		t.Error("the layout with the SMP virtual-443 mapping must be current")
	}
}

func TestEnsureSMPWebPageMigration(t *testing.T) {
	eng, paths := newWebTestEngine(t)
	if err := eng.State.UpdateServer(KindSMP, func(r *ServerRec) { r.OnionHost = "abc.onion" }); err != nil {
		t.Fatal(err)
	}

	// a v1.6.28 installation: no [WEB] https key, no certificate
	if eng.smpIniHasWebHttps() {
		t.Fatal("precondition: the fake INI must not have the https key")
	}
	eng.EnsureSMPWebPage()

	iniPath := filepath.Join(paths.CfgDir(KindSMP), "smp-server.ini")
	ini, err := LoadIni(iniPath)
	if err != nil {
		t.Fatal(err)
	}
	if got, ok := ini.Get("WEB", "https"); !ok || got != "8443" {
		t.Errorf("WEB https = %q (found=%v), want 8443", got, ok)
	}
	certPath, keyPath := webCertPaths(paths.CfgDir(KindSMP))
	if got, _ := ini.Get("WEB", "cert"); got != certPath {
		t.Errorf("WEB cert = %q, want %q", got, certPath)
	}
	if got, _ := ini.Get("WEB", "key"); got != keyPath {
		t.Errorf("WEB key = %q, want %q", got, keyPath)
	}
	if _, err := os.Stat(certPath); err != nil {
		t.Errorf("certificate missing after migration: %v", err)
	}
	if !certHostsCovered(certPath, []string{"abc.onion"}) {
		t.Error("certificate must cover the onion host")
	}
	if got := eng.State.Server(KindSMP).WebHttpsPort; got != 8443 {
		t.Errorf("state WebHttpsPort = %d, want 8443", got)
	}

	// the torrc written from now on maps virtual 443 to the page
	if err := eng.Tor.GenerateTorrc(); err != nil {
		t.Fatal(err)
	}
	torrc := readFileT(t, filepath.Join(paths.TorDir(), "torrc"))
	if !strings.Contains(torrc, "HiddenServicePort 443 127.0.0.1:8443") {
		t.Errorf("torrc must map virtual 443 after the migration:\n%s", torrc)
	}

	// idempotent: a second run changes nothing
	before, err := os.ReadFile(iniPath)
	if err != nil {
		t.Fatal(err)
	}
	eng.EnsureSMPWebPage()
	after, err := os.ReadFile(iniPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Error("second EnsureSMPWebPage run must not change the INI")
	}
}

func TestSyncSMPWebPageCertFollowsHosts(t *testing.T) {
	eng, paths := newWebTestEngine(t)
	if err := eng.State.UpdateServer(KindSMP, func(r *ServerRec) { r.OnionHost = "aaa.onion" }); err != nil {
		t.Fatal(err)
	}
	eng.SyncSMPWebPage()
	certPath, _ := webCertPaths(paths.CfgDir(KindSMP))
	if !certHostsCovered(certPath, []string{"aaa.onion"}) {
		t.Fatal("certificate must cover the first onion host")
	}

	// a new onion host (single-hop switch regenerates the service key)
	// or a public host added in settings: the certificate follows
	if err := eng.State.UpdateServer(KindSMP, func(r *ServerRec) { r.OnionHost = "bbb.onion" }); err != nil {
		t.Fatal(err)
	}
	eng.SyncSMPWebPage()
	if !certHostsCovered(certPath, []string{"bbb.onion"}) {
		t.Error("certificate must cover the new onion host after sync")
	}
	if certHostsCovered(certPath, []string{"aaa.onion"}) {
		t.Error("old host must not be required any more")
	}
}

func readFileT(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}
