package core

import (
        "crypto/rand"
        "crypto/rsa"
        "crypto/x509"
        "crypto/x509/pkix"
        "encoding/pem"
        "math/big"
        "net"
        "os"
        "path/filepath"
        "sort"
        "time"
)

// The SMP server's embedded HTTPS web page.
//
// simplexmq smp-server can serve its generated static mini-site (the
// server info page at "/" plus the official SimpleX invite/link landing
// pages at /contact, /invitation, /a, /c, /g, /r, /i - complete with the
// QR code rendered in the browser from the link's #fragment) over TLS:
// [WEB] https = <port>, cert = <file>, key = <file>. Short links
// (https://<server>/a#key) carry no port, so browsers connect to 443 -
// sxops maps the onion service's virtual port 443 to the local HTTPS
// web port (tor.go), which is what makes a link pasted into Tor Browser
// show the invite page instead of "connection refused".
//
// No public CA signs certificates for .onion names, so the certificate
// is self-signed (Tor Browser shows its standard "accept the risk"
// interstitial once, then remembers it). The SAN list still carries
// every advertised host so the certificate at least MATCHES the name
// the browser connected to. The file names match the upstream init
// template's defaults (<cfg>/web.crt, <cfg>/web.key).

const (
        webCertFile = "web.crt"
        webKeyFile  = "web.key"
        // RSA: the PEM encoding (PKCS#1 "RSA PRIVATE KEY") is read by every
        // TLS stack incl. the Haskell warp-tls server, unlike PKCS#8 EC keys.
        // 4096 bits: smp-server REJECTS weaker web credentials at start
        // (Server/Env/STM.hs checkHTTPSCredentials demands an RSA public
        // key of >= 512 bytes = 4096 bits and exitFailure's otherwise -
        // v1.6.29 shipped 2048-bit certs, which broke the server start).
        webCertBits  = 4096
        webCertYears = 10
)

// webCertHosts lists the hostnames the HTTPS web certificate should
// cover: the public host (when configured) and the SMP onion address.
func webCertHosts(s Settings, rec *ServerRec) []string {
        var hosts []string
        if s.PublicHost != "" {
                hosts = append(hosts, s.PublicHost)
        }
        if rec != nil && rec.OnionHost != "" {
                hosts = append(hosts, rec.OnionHost)
        }
        return dedupHosts(hosts)
}

func dedupHosts(in []string) []string {
        seen := map[string]bool{}
        var out []string
        for _, h := range in {
                if h == "" || seen[h] {
                        continue
                }
                seen[h] = true
                out = append(out, h)
        }
        sort.Strings(out)
        return out
}

// webCertPaths returns the absolute cert/key paths for a config dir.
func webCertPaths(cfgDir string) (string, string) {
        return filepath.Join(cfgDir, webCertFile), filepath.Join(cfgDir, webKeyFile)
}

// webCertExists reports whether the web certificate is in place.
func webCertExists(cfgDir string) bool {
        cert, key := webCertPaths(cfgDir)
        _, errC := os.Stat(cert)
        _, errK := os.Stat(key)
        return errC == nil && errK == nil
}

// ensureWebCert creates or replaces the self-signed certificate for the
// HTTPS web page so that it covers hosts. It returns true when the
// certificate was (re)generated - callers use that to restart a running
// server (the smp-server reads cert files at start). With no hosts at
// all (fresh install before the onion address is known) nothing is
// generated: the HTTPS page is enabled later, when the host is known.
func ensureWebCert(cfgDir string, hosts []string) (bool, error) {
        certPath, keyPath := webCertPaths(cfgDir)
        if len(hosts) == 0 {
                return false, nil
        }
        if certHostsCovered(certPath, hosts) {
                return false, nil
        }
        key, err := rsa.GenerateKey(rand.Reader, webCertBits)
        if err != nil {
                return false, err
        }
        serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 120))
        if err != nil {
                return false, err
        }
        tmpl := &x509.Certificate{
                SerialNumber:          serial,
                Subject:               pkix.Name{CommonName: hosts[0]},
                NotBefore:             time.Now().Add(-24 * time.Hour),
                NotAfter:              time.Now().AddDate(webCertYears, 0, 0),
                KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
                ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
                BasicConstraintsValid: true,
        }
        for _, h := range hosts {
                if ip := net.ParseIP(h); ip != nil {
                        tmpl.IPAddresses = append(tmpl.IPAddresses, ip)
                } else {
                        tmpl.DNSNames = append(tmpl.DNSNames, h)
                }
        }
        der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
        if err != nil {
                return false, err
        }
        if err := os.MkdirAll(cfgDir, 0o700); err != nil {
                return false, err
        }
        certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
        keyPEM := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
        if err := os.WriteFile(keyPath, keyPEM, 0o600); err != nil {
                return false, err
        }
        if err := os.WriteFile(certPath, certPEM, 0o644); err != nil {
                return false, err
        }
        return true, nil
}

// certHostsCovered reports whether the certificate at path exists, lists
// every host in its SANs (or CN), AND carries an RSA key of at least 4096
// bits (the smp-server's own start-up requirement for web credentials).
// An unreadable or unparseable certificate counts as not covered, so it
// gets replaced. This is also what heals v1.6.29 installs: their 2048-bit
// certificate is regenerated as 4096-bit on the first v1.6.30 run.
func certHostsCovered(certPath string, hosts []string) bool {
        data, err := os.ReadFile(certPath)
        if err != nil {
                return false
        }
        block, _ := pem.Decode(data)
        if block == nil {
                return false
        }
        crt, err := x509.ParseCertificate(block.Bytes)
        if err != nil {
                return false
        }
        if time.Now().After(crt.NotAfter) {
                return false
        }
        if key, ok := crt.PublicKey.(*rsa.PublicKey); !ok || key.N.BitLen() < webCertBits {
                return false // smp-server refuses < 4096-bit RSA web credentials
        }
        covered := map[string]bool{crt.Subject.CommonName: true}
        for _, d := range crt.DNSNames {
                covered[d] = true
        }
        for _, ip := range crt.IPAddresses {
                covered[ip.String()] = true
        }
        for _, h := range hosts {
                if !covered[h] {
                        return false
                }
        }
        return true
}
