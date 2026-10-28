package core

import (
	"crypto/x509"
	"encoding/pem"
	"os"
	"path/filepath"
	"testing"
)

// The SMP server's embedded HTTPS web page: sxops maintains a
// self-signed certificate (web.crt/web.key in the config dir) whose SANs
// cover the advertised hosts, so the certificate at least matches the
// name a browser connected to (no CA signs .onion names - Tor Browser
// shows its standard accept-the-risk interstitial once).

func TestEnsureWebCertGeneratesAndCovers(t *testing.T) {
	dir := t.TempDir()
	if regen, err := ensureWebCert(dir, nil); err != nil || regen {
		t.Fatalf("no hosts: regen=%v err=%v (nothing must be generated)", regen, err)
	}
	if webCertExists(dir) {
		t.Fatal("certificate must not exist without hosts")
	}
	regen, err := ensureWebCert(dir, []string{"abc.onion"})
	if err != nil || !regen {
		t.Fatalf("first generation: regen=%v err=%v", regen, err)
	}
	certPath, keyPath := webCertPaths(dir)
	for _, p := range []string{certPath, keyPath} {
		if _, err := os.Stat(p); err != nil {
			t.Fatalf("expected file %s: %v", p, err)
		}
	}
	if regen, err := ensureWebCert(dir, []string{"abc.onion"}); err != nil || regen {
		t.Fatalf("covered host must not regenerate: regen=%v err=%v", regen, err)
	}
	// a new host (e.g. a public host configured later) regenerates
	regen, err = ensureWebCert(dir, []string{"abc.onion", "pub.example.com"})
	if err != nil || !regen {
		t.Fatalf("new host must regenerate: regen=%v err=%v", regen, err)
	}
	if !certHostsCovered(certPath, []string{"abc.onion", "pub.example.com"}) {
		t.Error("certificate must cover both hosts after regeneration")
	}
}

func TestWebCertHosts(t *testing.T) {
	s := Settings{PublicHost: "pub.example.com"}
	rec := &ServerRec{OnionHost: "abc.onion"}
	if got := webCertHosts(s, rec); len(got) != 2 {
		t.Errorf("webCertHosts = %v, want public + onion", got)
	}
	if got := webCertHosts(Settings{}, &ServerRec{}); len(got) != 0 {
		t.Errorf("webCertHosts with no hosts = %v, want empty", got)
	}
	// duplicates collapse
	if got := webCertHosts(Settings{PublicHost: "abc.onion"}, rec); len(got) != 1 {
		t.Errorf("webCertHosts dup = %v, want one entry", got)
	}
}

func TestWebCertParseableAndServerAuth(t *testing.T) {
	dir := t.TempDir()
	if _, err := ensureWebCert(dir, []string{"abc.onion"}); err != nil {
		t.Fatal(err)
	}
	certPath, _ := webCertPaths(dir)
	data, err := os.ReadFile(certPath)
	if err != nil {
		t.Fatal(err)
	}
	block, _ := pem.Decode(data)
	if block == nil || block.Type != "CERTIFICATE" {
		t.Fatalf("web.crt is not a PEM CERTIFICATE: %v", block)
	}
	crt, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatalf("certificate does not parse: %v", err)
	}
	found := false
	for _, d := range crt.DNSNames {
		if d == "abc.onion" {
			found = true
		}
	}
	if !found {
		t.Errorf("SANs = %v, want abc.onion", crt.DNSNames)
	}
	if len(crt.ExtKeyUsage) != 1 || crt.ExtKeyUsage[0] != x509.ExtKeyUsageServerAuth {
		t.Errorf("ExtKeyUsage = %v, want ServerAuth only", crt.ExtKeyUsage)
	}
	// an IP host lands in IP SANs, not DNS SANs
	dir2 := t.TempDir()
	if _, err := ensureWebCert(dir2, []string{"192.168.1.10"}); err != nil {
		t.Fatal(err)
	}
	c2, _ := webCertPaths(dir2)
	d2, _ := os.ReadFile(c2)
	b2, _ := pem.Decode(d2)
	crt2, err := x509.ParseCertificate(b2.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	if len(crt2.IPAddresses) != 1 || crt2.IPAddresses[0].String() != "192.168.1.10" {
		t.Errorf("IP SANs = %v, want 192.168.1.10", crt2.IPAddresses)
	}
}

func TestApplyWebHttps(t *testing.T) {
	cfgDir := t.TempDir()
	ini := NewIni(filepath.Join(cfgDir, "smp-server.ini"))
	ini.appendSection("WEB")
	ini.Set("WEB", "http", "8023")

	// no certificate yet: the https keys stay disabled
	applyWebHttps(ini, Settings{EnableInfoPages: true}, cfgDir)
	if _, ok := ini.Get("WEB", "https"); ok {
		t.Error("https key must stay disabled without a certificate")
	}

	// certificate in place: keys enabled with the default port
	if _, err := ensureWebCert(cfgDir, []string{"abc.onion"}); err != nil {
		t.Fatal(err)
	}
	applyWebHttps(ini, Settings{EnableInfoPages: true}, cfgDir)
	if got, ok := ini.Get("WEB", "https"); !ok || got != "8443" {
		t.Errorf("https = %q (found=%v), want 8443", got, ok)
	}
	certPath, keyPath := webCertPaths(cfgDir)
	if got, _ := ini.Get("WEB", "cert"); got != certPath {
		t.Errorf("cert = %q, want %q", got, certPath)
	}
	if got, _ := ini.Get("WEB", "key"); got != keyPath {
		t.Errorf("key = %q, want %q", got, keyPath)
	}

	// a port the conflict resolution moved is preserved
	ini2 := NewIni(filepath.Join(cfgDir, "smp-server.ini"))
	ini2.appendSection("WEB")
	ini2.Set("WEB", "https", "8450")
	applyWebHttps(ini2, Settings{EnableInfoPages: true}, cfgDir)
	if got, _ := ini2.Get("WEB", "https"); got != "8450" {
		t.Errorf("moved https port = %q, want 8450 preserved", got)
	}

	// info pages off: keys disabled again
	ini3 := NewIni(filepath.Join(cfgDir, "smp-server.ini"))
	ini3.appendSection("WEB")
	ini3.Set("WEB", "https", "8443")
	ini3.Set("WEB", "cert", certPath)
	ini3.Set("WEB", "key", keyPath)
	applyWebHttps(ini3, Settings{}, cfgDir)
	if _, ok := ini3.Get("WEB", "https"); ok {
		t.Error("https key must be disabled with info pages off")
	}
}
