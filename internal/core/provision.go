package core

import (
        "bytes"
        "context"
        "crypto/rand"
        "encoding/base64"
        "fmt"
        "os"
        "os/exec"
        "path/filepath"
        "strings"
        "time"
)

// randomToken generates a URL-safe random token of n bytes
// (same style as smp-server's control port tokens).
func randomToken(n int) string {
        b := make([]byte, n)
        if _, err := rand.Read(b); err != nil {
                panic(err)
        }
        return base64.RawURLEncoding.EncodeToString(b)
}

// Provisioner initialises server configs under the portable profile.
type Provisioner struct {
        Paths *Paths
        State *StateStore
        Log   InstallLog
}

// NewProvisioner creates a provisioner.
func NewProvisioner(paths *Paths, state *StateStore, log InstallLog) *Provisioner {
        if log == nil {
                log = NopInstallLog()
        }
        return &Provisioner{Paths: paths, State: state, Log: log}
}

// EnsureOpenSSL verifies the openssl binary is available (server init
// shells out to it for certificate generation).
func EnsureOpenSSL() error {
        if _, err := exec.LookPath("openssl"); err != nil {
                return fmt.Errorf("openssl is required by server initialization but was not found in PATH")
        }
        return nil
}

// Provision initializes a server kind: runs `init`, patches the INI for
// the portable profile and records fingerprints and control tokens.
func (pr *Provisioner) Provision(ctx context.Context, k Kind) error {
        if k == KindRelay {
                return pr.provisionRelay()
        }
        d := Def(k)
        rec := pr.State.Server(k)
        if !rec.Installed {
                return fmt.Errorf("%s binary is not installed yet", d.Name)
        }
        if err := EnsureOpenSSL(); err != nil {
                return err
        }

        iniPath := filepath.Join(pr.Paths.CfgDir(k), d.IniName)
        if _, err := os.Stat(iniPath); err == nil {
                return fmt.Errorf("%s is already initialized (%s exists)", d.Name, iniPath)
        }

        s := pr.State.Settings()
        args, err := InitArgs(k, s, pr.Paths)
        if err != nil {
                return err
        }
        pr.Log.Step(fmt.Sprintf("Initializing %s (generating %s keys and certificates)", d.Name, "Ed25519"))
        cmd := exec.CommandContext(ctx, pr.Paths.BinPath(k), args...)
        cmd.Env = append(os.Environ(),
                d.CfgEnvVar+"="+pr.Paths.CfgDir(k),
                d.LogEnvVar+"="+pr.Paths.LogDir(k),
        )
        var out bytes.Buffer
        cmd.Stdout = &out
        cmd.Stderr = &out
        if err := cmd.Run(); err != nil {
                return fmt.Errorf("init failed: %v\n%s", err, truncate(out.String(), 2000))
        }
        pr.Log.Detail(firstLine(out.String()))

        // Patch the INI for the portable, unprivileged profile.
        pr.Log.Step("Applying portable profile to configuration")
        ini, err := LoadIni(iniPath)
        if err != nil {
                return err
        }
        applyPortableProfile(ini, k, s, rec,
                ClientSocksPortOf(pr.State), ntfDBConnStr(pr.State, pr.Paths))
        if k == KindSMP {
                // v1.6.29: the embedded HTTPS web page (the certificate
                // only exists once a host - public or onion - is known;
                // setOnionHosts enables the keys then). With a public host
                // configured at setup the certificate is ready right here.
                if _, err := ensureWebCert(pr.Paths.CfgDir(k), webCertHosts(s, rec)); err != nil {
                        pr.Log.Detail("web page certificate: " + err.Error())
                }
                applyWebHttps(ini, s, pr.Paths.CfgDir(k))
        }
        if err := ini.Save(); err != nil {
                return err
        }

        // Read fingerprint and control tokens.
        fpPath := filepath.Join(pr.Paths.CfgDir(k), "fingerprint")
        fp, err := os.ReadFile(fpPath)
        if err != nil {
                return fmt.Errorf("fingerprint file missing after init: %v", err)
        }
        admin, _ := ini.Get("AUTH", "control_port_admin_password")
        user, _ := ini.Get("AUTH", "control_port_user_password")

        return pr.State.UpdateServer(k, func(r *ServerRec) {
                r.Fingerprint = strings.TrimSpace(string(fp))
                r.ControlAdmin = admin
                r.ControlUser = user
                r.Port = DefaultPort(k)
                r.ControlPort = DefaultControlPort(k)
                r.WebPort = DefaultWebPort(k)
                r.WebHttpsPort = DefaultWebHttpsPort(k) // SMP only (0 for others)
        })
}

// provisionRelay prepares the chat relay data directory.
func (pr *Provisioner) provisionRelay() error {
        return os.MkdirAll(pr.Paths.RelayDir(), 0o700)
}

// SetOnionHost records the onion hostname in state and in the server INI
// so the server prints it as part of its address.
func (pr *Provisioner) SetOnionHost(k Kind, onion string) error {
        if onion == "" {
                return nil
        }
        if err := pr.State.UpdateServer(k, func(r *ServerRec) { r.OnionHost = onion }); err != nil {
                return err
        }
        d := Def(k)
        if d.IniName == "" {
                return nil
        }
        iniPath := filepath.Join(pr.Paths.CfgDir(k), d.IniName)
        if _, err := os.Stat(iniPath); err != nil {
                return nil // not initialized yet
        }
        ini, err := LoadIni(iniPath)
        if err != nil {
                return err
        }
        s := pr.State.Settings()
        // host order: public (if any), then the onion.
        var hs []string
        if s.PublicHost != "" {
                hs = append(hs, s.PublicHost)
        }
        hs = append(hs, onion)
        ini.Set("TRANSPORT", "host", strings.Join(hs, ","))
        return ini.Save()
}

// clientDefaultPort is the port a SimpleX client connects to when an
// address carries no explicit port. Verified against the simplexmq
// client source (stable): the SMP protocol client defaults to 5223
// (defaultSMPClientConfig), the XFTP client to 443 ("if null port then
// \"443\"") and the notification client to 443 (defaultTransport).
// sxops maps these as additional VIRTUAL ports on every onion service
// (see TorManager.GenerateTorrc), so onion-only addresses are rendered
// without a port - as short as the official ones.
func clientDefaultPort(k Kind) int {
        switch k {
        case KindSMP:
                return 5223
        case KindXFTP, KindNTF:
                return 443
        }
        return 0
}

// Address builds the client-facing address for a server kind:
//
//      smp://[fingerprint][:password]@host[,onion][:port]
//
// v1.6.3: ONION-ONLY addresses (no public host configured) carry no
// port at all - the clients' protocol defaults (5223 / 443, see
// clientDefaultPort) are routed to the server as extra virtual ports on
// its onion service, so the links work port-free. Addresses shared
// before this change (with the explicit port) keep working unchanged:
// the canonical virtual ports stay mapped too. Public-host mode still
// appends the port whenever it differs from the protocol default,
// because the public port is only reachable under that number.
func Address(k Kind, s Settings, rec *ServerRec) string {
        if k == KindRelay {
                return "" // relay address comes from the process itself
        }
        if rec == nil || rec.Fingerprint == "" {
                return ""
        }
        var hosts []string
        if s.PublicHost != "" {
                h := s.PublicHost
                if s.PublicPort != 0 && s.PublicPort != 5223 {
                        h = fmt.Sprintf("%s:%d", s.PublicHost, s.PublicPort)
                }
                hosts = append(hosts, h)
        }
        if rec.OnionHost != "" {
                hosts = append(hosts, rec.OnionHost)
        }
        if len(hosts) == 0 {
                hosts = append(hosts, "YOUR-SERVER-HOST")
        }
        auth := rec.Fingerprint
        if s.PasswordProtect && s.QueuePassword != "" && k != KindNTF {
                auth += ":" + s.QueuePassword
        }
        onionOnly := s.PublicHost == ""
        hostPart := strings.Join(hosts, ",")
        // port suffix: onion-only links never carry one; public-host
        // links only when the advertised port differs from the protocol
        // default (a default port in the address would be redundant).
        withPort := func() string {
                if onionOnly || rec.Port == 0 || rec.Port == clientDefaultPort(k) {
                        return ""
                }
                return fmt.Sprintf(":%d", rec.Port)
        }
        switch k {
        case KindSMP:
                return "smp://" + auth + "@" + hostPart + withPort()
        case KindXFTP:
                return "xftp://" + auth + "@" + hostPart + withPort()
        case KindNTF:
                return "ntf://" + auth + "@" + hostPart + withPort()
        }
        return ""
}

// InitOutputAddress extracts the "Server address:" line from init output.
func InitOutputAddress(out string) string {
        for _, line := range strings.Split(out, "\n") {
                if strings.HasPrefix(strings.TrimSpace(line), "Server address:") {
                        return strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "Server address:"))
                }
        }
        return ""
}

func truncate(s string, n int) string {
        if len(s) <= n {
                return s
        }
        return s[:n] + "..."
}

func firstLine(s string) string {
        for _, line := range strings.Split(s, "\n") {
                if t := strings.TrimSpace(line); t != "" {
                        return t
                }
        }
        return ""
}

// Retry helper used by provisioning flows.
func waitUntil(ctx context.Context, timeout time.Duration, interval time.Duration, fn func() (bool, error)) error {
        deadline := time.Now().Add(timeout)
        for {
                ok, err := fn()
                if err != nil {
                        return err
                }
                if ok {
                        return nil
                }
                if time.Now().After(deadline) {
                        return fmt.Errorf("timed out after %s", timeout)
                }
                select {
                case <-ctx.Done():
                        return ctx.Err()
                case <-time.After(interval):
                }
        }
}
