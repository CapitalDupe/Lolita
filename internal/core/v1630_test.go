package core

import (
        "crypto/rand"
        "crypto/rsa"
        "crypto/x509"
        "crypto/x509/pkix"
        "encoding/pem"
        "math/big"
        "os"
        "path/filepath"
        "testing"
        "time"
)

// v1.6.30: the smp-server rejects web HTTPS credentials with an RSA key
// below 4096 bits at start-up (Server/Env/STM.hs checkHTTPSCredentials:
// RSA.public_size >= 512 bytes, else "Error: unsupported HTTPS
// credentials, required 4096-bit RSA" + exitFailure). v1.6.29 generated
// 2048-bit certificates, which left the SMP server unable to come up
// after the web-page migration. v1.6.30 generates 4096-bit certificates
// and heals existing 2048-bit ones.

// webCertKeyBits returns the RSA modulus size of the certificate at path
// (0 when the file is missing or not an RSA certificate).
func webCertKeyBits(certPath string) int {
        data, err := os.ReadFile(certPath)
        if err != nil {
                return 0
        }
        block, _ := pem.Decode(data)
        if block == nil {
                return 0
        }
        crt, err := x509.ParseCertificate(block.Bytes)
        if err != nil {
                return 0
        }
        key, ok := crt.PublicKey.(*rsa.PublicKey)
        if !ok {
                return 0
        }
        return key.N.BitLen()
}

// plantWeakWebCert writes a v1.6.29-style 2048-bit certificate covering
// hosts (same SAN structure as ensureWebCert produces).
func plantWeakWebCert(t *testing.T, cfgDir string, hosts []string) {
        t.Helper()
        key, err := rsa.GenerateKey(rand.Reader, 2048)
        if err != nil {
                t.Fatal(err)
        }
        tmpl := &x509.Certificate{
                SerialNumber: big.NewInt(1),
                Subject:      pkix.Name{CommonName: hosts[0]},
                NotBefore:    time.Now().Add(-time.Hour),
                NotAfter:     time.Now().Add(24 * time.Hour),
        }
        for _, h := range hosts {
                tmpl.DNSNames = append(tmpl.DNSNames, h)
        }
        der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
        if err != nil {
                t.Fatal(err)
        }
        certPath, keyPath := webCertPaths(cfgDir)
        if err := os.WriteFile(keyPath, pem.EncodeToMemory(&pem.Block{
                Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)}), 0o600); err != nil {
                t.Fatal(err)
        }
        if err := os.WriteFile(certPath, pem.EncodeToMemory(&pem.Block{
                Type: "CERTIFICATE", Bytes: der}), 0o644); err != nil {
                t.Fatal(err)
        }
}

func TestWebCertMeetsServer4096Requirement(t *testing.T) {
        dir := t.TempDir()
        if _, err := ensureWebCert(dir, []string{"abc.onion"}); err != nil {
                t.Fatal(err)
        }
        certPath, _ := webCertPaths(dir)
        if bits := webCertKeyBits(certPath); bits < 4096 {
                t.Fatalf("web certificate key = %d bits, want >= 4096 (the smp-server refuses weaker credentials at start)", bits)
        }
}

func TestCertHostsCoveredRejectsWeakKey(t *testing.T) {
        dir := t.TempDir()
        plantWeakWebCert(t, dir, []string{"abc.onion"})
        certPath, _ := webCertPaths(dir)
        if bits := webCertKeyBits(certPath); bits != 2048 {
                t.Fatalf("test setup: planted certificate = %d bits, want 2048", bits)
        }
        // SANs cover the host, but the 2048-bit key is rejected: the
        // v1.6.29 state must be recognized as broken so it heals.
        if certHostsCovered(certPath, []string{"abc.onion"}) {
                t.Error("certHostsCovered must reject a 2048-bit certificate even when SANs cover the hosts")
        }
        // ensureWebCert replaces it with a 4096-bit certificate
        regen, err := ensureWebCert(dir, []string{"abc.onion"})
        if err != nil || !regen {
                t.Fatalf("weak certificate must regenerate: regen=%v err=%v", regen, err)
        }
        if bits := webCertKeyBits(certPath); bits < 4096 {
                t.Errorf("regenerated certificate key = %d bits, want >= 4096", bits)
        }
        if !certHostsCovered(certPath, []string{"abc.onion"}) {
                t.Error("regenerated certificate must be covered")
        }
}

func TestEnsureSMPWebPageHealsBrokenV1629Cert(t *testing.T) {
        eng, paths := newWebTestEngine(t)
        if err := eng.State.UpdateServer(KindSMP, func(r *ServerRec) { r.OnionHost = "abc.onion" }); err != nil {
                t.Fatal(err)
        }
        // a healthy v1.6.30 state first (the migration ran once)
        eng.EnsureSMPWebPage()
        iniPath := filepath.Join(paths.CfgDir(KindSMP), "smp-server.ini")
        beforeIni, err := os.ReadFile(iniPath)
        if err != nil {
                t.Fatal(err)
        }
        if got, ok := func() (string, bool) {
                ini, err := LoadIni(iniPath)
                if err != nil {
                        t.Fatal(err)
                }
                return ini.Get("WEB", "https")
        }(); !ok || got != "8443" {
                t.Fatalf("precondition: WEB https = %q (found=%v), want 8443", got, ok)
        }

        // now simulate the broken v1.6.29 certificate: the INI has the
        // https keys but the certificate is 2048-bit
        plantWeakWebCert(t, paths.CfgDir(KindSMP), []string{"abc.onion"})
        certPath, _ := webCertPaths(paths.CfgDir(KindSMP))
        if webCertKeyBits(certPath) != 2048 {
                t.Fatal("test setup: planted certificate must be 2048-bit")
        }

        // the one-time migration must HEAL this state, not skip it
        eng.EnsureSMPWebPage()
        if bits := webCertKeyBits(certPath); bits < 4096 {
                t.Fatalf("EnsureSMPWebPage must replace the broken 2048-bit certificate, got %d bits", bits)
        }
        afterIni, err := os.ReadFile(iniPath)
        if err != nil {
                t.Fatal(err)
        }
        if string(beforeIni) != string(afterIni) {
                t.Error("the heal must not rewrite the INI (only the certificate was broken)")
        }
        if !certHostsCovered(certPath, []string{"abc.onion"}) {
                t.Error("healed certificate must cover the onion host")
        }

        // and the healed state is idempotent again
        eng.EnsureSMPWebPage()
        if bits := webCertKeyBits(certPath); bits < 4096 {
                t.Errorf("second run must keep the 4096-bit certificate, got %d bits", bits)
        }
}
