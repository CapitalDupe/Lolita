package core

import (
        "context"
        "encoding/hex"
        "fmt"
        "io"
        "net"
        "os"
        "os/exec"
        "path/filepath"
        "regexp"
        "strconv"
        "strings"
        "sync"
        "time"
)

// TorManager runs a dedicated, unprivileged Tor instance that publishes
// onion services for the managed servers - the "no port forwarding"
// transport. The generated torrc follows the pattern recommended by the
// SimpleX server docs (standard 3-hop routing for onion-only servers,
// optional single-hop for lower latency when combined with a public host).
//
// Single-hop mode (HiddenServiceSingleHopMode) fundamentally requires
// SocksPort 0 on the service instance (tor rejects any client port in
// non-anonymous mode), so sxops then runs a SECOND, client-only Tor
// instance for its own client needs: the chat relay's address
// registration, the onion reachability probes and the SOCKS port offered
// to the SimpleX desktop app on this machine.
//
// Tor itself logs to its notice log file and writes a pidfile, so it
// survives sxops restarts; a later instance adopts it via the control
// port / pidfile.
type TorManager struct {
        Paths     *Paths
        State     *StateStore
        Log       InstallLog
        mu        sync.Mutex
        cmd       *exec.Cmd
        stopCh    chan struct{}
        stopping  bool
        exited    bool
        startedAt time.Time
        // restarts counts consecutive crash-restarts that did not reach a
        // successful bootstrap. Tor that keeps exiting (bad config, port
        // conflict) is relaunched at most maxTorRestarts times before sxops
        // gives up and reports the failure instead of flapping forever.
        restarts int
        // client-only instance (single-hop mode only)
        clientCmd      *exec.Cmd
        clientStopCh   chan struct{}
        clientStopping bool
        clientExited   bool
}

// maxTorRestarts bounds the automatic relaunches of a dying Tor process.
const maxTorRestarts = 5

// NewTorManager creates a Tor manager.
func NewTorManager(paths *Paths, state *StateStore, log InstallLog) *TorManager {
        if log == nil {
                log = NopInstallLog()
        }
        return &TorManager{Paths: paths, State: state, Log: log, stopCh: make(chan struct{})}
}

// TorStatus is a snapshot of the managed Tor instance.
type TorStatus struct {
        Running      bool
        PID          int
        Started      time.Time
        Bootstrapped bool
        BootstrapMsg string
        SocksPort    int
        ControlPort  int
        // ClientSocksPort is the SOCKS port to hand to local clients (the
        // client-only instance's port in single-hop mode, the shared port
        // otherwise).
        ClientSocksPort int
}

// ClientSocksPort returns the SOCKS port sxops and local SimpleX apps
// should use for CLIENT connections through the managed Tor: the
// dedicated client instance's port in single-hop mode, the shared port
// of the single instance otherwise.
func ClientSocksPortOf(state *StateStore) int {
        tor := state.Get().Tor
        if state.Settings().SingleHopOnion && tor.ClientSocksPort != 0 {
                return tor.ClientSocksPort
        }
        return tor.SocksPort
}

// ClientSocksPort is ClientSocksPortOf for this manager's state.
func (tm *TorManager) ClientSocksPort() int { return ClientSocksPortOf(tm.State) }

// FindTor locates a tor binary: managed install first, then PATH.
func (tm *TorManager) FindTor() (string, error) {
        candidates := []string{tm.Paths.TorBinPath()}
        if p, err := exec.LookPath("tor"); err == nil {
                candidates = append(candidates, p)
        }
        for _, c := range candidates {
                if st, err := os.Stat(c); err == nil && !st.IsDir() && st.Mode()&0o111 != 0 {
                        return c, nil
                }
        }
        return "", fmt.Errorf("Tor is not installed - install the 'tor' package (e.g. sudo apt install tor) or see the Tor panel")
}

// pickPorts selects free socks/control ports (9050/9051 with fallbacks).
func pickPorts(preferSocks, preferCtrl int) (socks, ctrl int, err error) {
        free := func(p int) bool { return tcpPortFree(p) }
        socks = preferSocks
        for socks < preferSocks+20 && !free(socks) {
                socks++
        }
        ctrl = preferCtrl
        for ctrl < preferCtrl+20 && (!free(ctrl) || ctrl == socks) {
                ctrl++
        }
        if !free(socks) || !free(ctrl) {
                return 0, 0, fmt.Errorf("no free ports for Tor around %d/%d", preferSocks, preferCtrl)
        }
        return socks, ctrl, nil
}

// tcpPortFree reports whether 127.0.0.1:p can be bound right now.
func tcpPortFree(p int) bool {
        if p <= 0 || p > 65535 {
                return false
        }
        l, err := net.Listen("tcp", "127.0.0.1:"+strconv.Itoa(p))
        if err != nil {
                return false
        }
        l.Close()
        return true
}

// torrcPath is the generated tor configuration.
func (tm *TorManager) torrcPath() string { return filepath.Join(tm.Paths.TorDir(), "torrc") }

// portsUsable reports whether the pinned socks/control ports can still
// be used by the managed Tor: either both are free right now (Tor is
// not running), or they belong to our own live instance (in-process or
// adopted from a previous sxops run). A foreign listener - typically
// the distro Tor service started by installing the "tor" package -
// makes them unusable and forces a re-pick.
func (tm *TorManager) portsUsable(socks, ctrl int) bool {
        if tm.Running() {
                return true
        }
        return tcpPortFree(socks) && tcpPortFree(ctrl)
}

// hiddenServiceKinds returns the kinds that should get onion services
// (relay excluded: it is a client-side agent).
func hiddenServiceKinds() []Kind { return []Kind{KindSMP, KindXFTP, KindNTF} }

// NormalizeBridgeLines cleans up bridge lines pasted by a user: strips a
// leading "bridge" keyword, surrounding whitespace and empty lines.
func NormalizeBridgeLines(raw []string) []string {
        var out []string
        for _, l := range raw {
                l = strings.TrimSpace(l)
                if l == "" || strings.HasPrefix(l, "#") {
                        continue
                }
                if strings.HasPrefix(strings.ToLower(l), "bridge ") {
                        l = strings.TrimSpace(l[len("bridge "):])
                }
                if l == "" {
                        continue
                }
                out = append(out, l)
        }
        return out
}

// ValidateBridges checks that the bridge lines can actually be used by the
// installed Tor: obfs4/obfs3 bridges need the obfs4proxy package, other
// exotic transports are not supported. Plain "IP:port" (vanilla) bridges
// work with stock Tor.
func ValidateBridges(bridges []string) error {
        needObfs := false
        for _, br := range bridges {
                fields := strings.Fields(br)
                if len(fields) == 0 {
                        continue
                }
                switch strings.ToLower(fields[0]) {
                case "obfs4", "obfs3":
                        needObfs = true
                case "snowflake":
                        return fmt.Errorf("snowflake bridges are not supported (no snowflake-client in Ubuntu/Debian repositories)")
                case "meek_lite", "meek":
                        return fmt.Errorf("meek bridges are not supported")
                case "webtunnel":
                        return fmt.Errorf("webtunnel bridges are not supported")
                }
        }
        if needObfs {
                if _, err := exec.LookPath("obfs4proxy"); err != nil {
                        return fmt.Errorf("obfs4 bridges need the obfs4proxy package.\n" +
                                "Install it in a terminal:  sudo apt install obfs4proxy\n" +
                                "(on Fedora: sudo dnf install obfs4proxy), then try again.")
                }
        }
        return nil
}

// GenerateTorrc writes the managed torrc for the enabled servers, and
// (in single-hop mode) the torrc of the client-only Tor instance.
func (tm *TorManager) GenerateTorrc() error {
        s := tm.State.Settings()
        tor := tm.State.Get().Tor
        socks, ctrl := 9050, 9051
        if tor.SocksPort != 0 && tm.portsUsable(tor.SocksPort, tor.ControlPort) {
                // keep the pinned ports: they are free (Tor not running
                // right now) or held by our own live instance
                socks, ctrl = tor.SocksPort, tor.ControlPort
        } else {
                var err error
                socks, ctrl, err = pickPorts(socks, ctrl)
                if err != nil {
                        return err
                }
                _ = tm.State.With(func(st *State) {
                        st.Tor.SocksPort = socks
                        st.Tor.ControlPort = ctrl
                        st.Tor.Managed = true
                })
                if tor.SocksPort != 0 {
                        // typical cause: installing the "tor" package starts
                        // the distro's own Tor service on 9050/9051
                        tm.Log.Detail(fmt.Sprintf(
                                "ports %d/%d are used by another program (the system Tor service?) - sxops uses %d/%d instead",
                                tor.SocksPort, tor.ControlPort, socks, ctrl))
                }
        }

        var b strings.Builder
        b.WriteString("# Managed by sxops - SimpleX Server Ops\n")
        b.WriteString("# Onion services publish your servers without any port forwarding.\n\n")
        b.WriteString("DataDirectory " + tm.Paths.TorDataDir() + "\n")
        b.WriteString("PidFile " + tm.Paths.TorPidFile() + "\n")
        if s.SingleHopOnion {
                // Single-hop (non-anonymous) onion services. Tor REQUIRES the
                // service instance to run without any client port here - verified
                // against tor 0.4.9: "HiddenServiceNonAnonymousMode is incompatible
                // with using Tor as an anonymous client. Please set Socks/Trans/"
                // "NATD/DNSPort to 0". sxops' own client needs a second instance
                // (torrc-client) that runs in normal anonymous client mode.
                b.WriteString("SocksPort 0\n")
        } else {
                b.WriteString("SocksPort 127.0.0.1:" + strconv.Itoa(socks) + "\n")
        }
        b.WriteString("ControlPort 127.0.0.1:" + strconv.Itoa(ctrl) + "\n")
        b.WriteString("CookieAuthentication 1\n")
        b.WriteString("Log notice file " + tm.Paths.TorLogFile() + "\n\n")
        // Tor bridges (for censored networks): only connect through the listed
        // bridges instead of the public Tor network.
        if len(s.TorBridges) > 0 {
                b.WriteString("# Tor bridges - used instead of a direct connection to the Tor network\n")
                b.WriteString("UseBridges 1\n")
                obfs3, obfs4 := false, false
                for _, br := range s.TorBridges {
                        b.WriteString("Bridge " + br + "\n")
                        switch strings.ToLower(strings.Fields(br)[0]) {
                        case "obfs3":
                                obfs3 = true
                        case "obfs4":
                                obfs4 = true
                        }
                }
                if p, err := exec.LookPath("obfs4proxy"); err == nil {
                        if obfs4 {
                                b.WriteString("ClientTransportPlugin obfs4 exec " + p + "\n")
                        }
                        if obfs3 {
                                b.WriteString("ClientTransportPlugin obfs3 exec " + p + "\n")
                        }
                }
                b.WriteString("\n")
        }
        if s.SingleHopOnion {
                b.WriteString("# Single-hop onion routing: lower latency, reduced server anonymity\n")
                b.WriteString("# (recommended only when the onion address is used together with a public address)\n")
                b.WriteString("HiddenServiceNonAnonymousMode 1\n")
                b.WriteString("HiddenServiceSingleHopMode 1\n\n")
        }
        for _, k := range hiddenServiceKinds() {
                rec := tm.State.Server(k)
                if !rec.Installed {
                        continue
                }
                dir := tm.Paths.HiddenServiceDir(k)
                if err := os.MkdirAll(dir, 0o700); err != nil {
                        return err
                }
                b.WriteString("# " + Def(k).Name + " onion service\n")
                b.WriteString("HiddenServiceDir " + dir + "\n")
                // The VIRTUAL port stays at the canonical value used in
                // the advertised address; the LOCAL target follows the
                // port the server actually binds on 127.0.0.1 (moved by
                // the automatic port-conflict resolution when the
                // canonical port was taken by another program).
                virtual := rec.Port
                if virtual == 0 {
                        virtual = DefaultPort(k)
                }
                local := rec.LocalPort
                if local == 0 {
                        local = virtual
                }
                b.WriteString(fmt.Sprintf("HiddenServicePort %d 127.0.0.1:%d\n", virtual, local))
                // v1.6.3: also map the PROTOCOL default virtual port (SMP
                // 5223, XFTP/NTF 443 - see clientDefaultPort) to the same
                // local target. Clients derive that port for addresses
                // without an explicit one, so the onion-only links sxops
                // renders stay port-free; the canonical virtual port above
                // keeps every already-shared address with its explicit
                // port working. Virtual ports cost nothing locally - Tor
                // does not bind them, it only routes onion connections
                // that target them.
                if def := clientDefaultPort(k); def != 0 && def != virtual {
                        b.WriteString(fmt.Sprintf("HiddenServicePort %d 127.0.0.1:%d\n", def, local))
                }
                if rec.WebPort > 0 && s.EnableInfoPages {
                        // Publish the info page at virtual port 80.
                        b.WriteString(fmt.Sprintf("HiddenServicePort 80 127.0.0.1:%d\n", rec.WebPort))
                }
                // v1.6.29: the SMP server's embedded HTTPS web page (the
                // official SimpleX invite/link landing pages). Short links
                // (https://<onion>/a#key) carry no port, so browsers connect
                // to virtual port 443 - mapped onto the local web port. The
                // fallback to the default covers the window before the INI
                // carries the key (the certificate only exists once a host
                // is known), so the mapping is right from Tor's first start.
                if k == KindSMP && s.EnableInfoPages {
                        webHttps := rec.WebHttpsPort
                        if webHttps == 0 {
                                webHttps = DefaultWebHttpsPort(KindSMP)
                        }
                        b.WriteString(fmt.Sprintf("HiddenServicePort 443 127.0.0.1:%d\n", webHttps))
                }
                b.WriteString("\n")
        }
        if err := os.MkdirAll(filepath.Dir(tm.torrcPath()), 0o700); err != nil {
                return err
        }
        _ = os.WriteFile(tm.torrcPath()+".prev", []byte(b.String()), 0o600)
        if err := os.WriteFile(tm.torrcPath(), []byte(b.String()), 0o600); err != nil {
                return err
        }
        if s.SingleHopOnion {
                return tm.generateClientTorrc(s)
        }
        // not in single-hop mode: a stale client torrc from an earlier
        // single-hop deployment would keep a second Tor running - remove it
        _ = os.Remove(tm.Paths.TorClientRcPath())
        _ = tm.State.With(func(st *State) {
                st.Tor.ClientSocksPort = 0
                st.Tor.ClientControlPort = 0
        })
        return nil
}

// generateClientTorrc writes the torrc of the client-only Tor instance
// used in single-hop mode (the service instance runs with SocksPort 0
// there, but sxops itself needs a SOCKS client for the relay registration
// and the onion reachability probes - and the SimpleX desktop app on this
// machine can use it too).
func (tm *TorManager) generateClientTorrc(s Settings) error {
        tor := tm.State.Get().Tor
        csocks, cctrl := 9060, 9061
        if tor.ClientSocksPort != 0 && tm.clientPortsUsable(tor.ClientSocksPort, tor.ClientControlPort) {
                csocks, cctrl = tor.ClientSocksPort, tor.ClientControlPort
        } else {
                var err error
                csocks, cctrl, err = pickPorts(csocks, cctrl)
                if err != nil {
                        return err
                }
                _ = tm.State.With(func(st *State) {
                        st.Tor.ClientSocksPort = csocks
                        st.Tor.ClientControlPort = cctrl
                })
        }
        var b strings.Builder
        b.WriteString("# Managed by sxops - client-only Tor instance (single-hop mode)\n")
        b.WriteString("# The onion-service instance runs in non-anonymous single-hop mode with\n")
        b.WriteString("# SocksPort 0; this anonymous client serves sxops' own connections.\n\n")
        b.WriteString("DataDirectory " + tm.Paths.TorClientDataDir() + "\n")
        b.WriteString("PidFile " + tm.Paths.TorClientPidFile() + "\n")
        b.WriteString("SocksPort 127.0.0.1:" + strconv.Itoa(csocks) + "\n")
        b.WriteString("ControlPort 127.0.0.1:" + strconv.Itoa(cctrl) + "\n")
        b.WriteString("CookieAuthentication 1\n")
        b.WriteString("Log notice file " + tm.Paths.TorClientLogFile() + "\n\n")
        if len(s.TorBridges) > 0 {
                b.WriteString("UseBridges 1\n")
                obfs3, obfs4 := false, false
                for _, br := range s.TorBridges {
                        b.WriteString("Bridge " + br + "\n")
                        switch strings.ToLower(strings.Fields(br)[0]) {
                        case "obfs3":
                                obfs3 = true
                        case "obfs4":
                                obfs4 = true
                        }
                }
                // v1.6.34: the transport plugin lines mirror the main torrc -
                // only the transports the bridge lines actually name, instead
                // of an unconditional obfs4 line that made vanilla-only setups
                // depend on obfs4proxy for no reason (and obfs3 bridges miss
                // their plugin line here entirely).
                if p, err := exec.LookPath("obfs4proxy"); err == nil {
                        if obfs4 {
                                b.WriteString("ClientTransportPlugin obfs4 exec " + p + "\n")
                        }
                        if obfs3 {
                                b.WriteString("ClientTransportPlugin obfs3 exec " + p + "\n")
                        }
                }
        }
        return os.WriteFile(tm.Paths.TorClientRcPath(), []byte(b.String()), 0o600)
}

// clientPortsUsable is portsUsable for the client instance ports.
func (tm *TorManager) clientPortsUsable(socks, ctrl int) bool {
        if tm.clientRunning() {
                return true
        }
        return tcpPortFree(socks) && tcpPortFree(ctrl)
}

// torrcHasDefaultPorts reports whether the on-disk torrc maps the protocol
// default virtual port for every installed server kind - the layout the
// v1.6.3 portless onion addresses rely on. Torrcs written by older sxops
// only map the canonical ports (5223/5225/5227), so a false return means
// the one-time portless-links migration (Engine.migratePortlessLinks)
// still has to run.
func (tm *TorManager) torrcHasDefaultPorts() bool {
        data, err := os.ReadFile(tm.torrcPath())
        if err != nil {
                return false // no torrc yet - GenerateTorrc will write the new layout
        }
        torrc := string(data)
        for _, k := range hiddenServiceKinds() {
                rec := tm.State.Server(k)
                if rec == nil || !rec.Installed {
                        continue
                }
                def := clientDefaultPort(k)
                if def == 0 {
                        continue
                }
                if !strings.Contains(torrc, fmt.Sprintf("HiddenServicePort %d ", def)) {
                        return false
                }
        }
        // v1.6.29: the SMP onion must also map virtual port 443 onto the
        // HTTPS web page when info pages are enabled. Scoped to the SMP
        // service block: XFTP/NTF blocks carry their own 443 line (their
        // protocol default port) which would mask a missing SMP mapping.
        if rec := tm.State.Server(KindSMP); rec != nil && rec.Installed && tm.State.Settings().EnableInfoPages {
                if !strings.Contains(torrcHiddenServiceBlock(torrc, KindSMP), "HiddenServicePort 443 ") {
                        return false
                }
        }
        return true
}

// torrcHiddenServiceBlock returns the hidden-service section GenerateTorrc
// wrote for one server kind ("# SMP server onion service" up to the next
// section comment), or "" when the block is absent.
func torrcHiddenServiceBlock(torrc string, k Kind) string {
        marker := "# " + Def(k).Name + " onion service"
        lines := strings.Split(torrc, "\n")
        var out []string
        inBlock := false
        for _, l := range lines {
                if strings.HasPrefix(l, "# ") {
                        if inBlock {
                                break // next section
                        }
                        inBlock = l == marker
                        continue
                }
                if inBlock {
                        out = append(out, l)
                }
        }
        return strings.Join(out, "\n")
}

// Start (re)starts the managed Tor process with a freshly generated
// torrc and waits for bootstrap. A running instance - ours, or adopted
// from a previous sxops run - is restarted so that config changes (new
// onion services, bridges, moved ports) actually take effect; a running
// Tor never re-reads its torrc. Restarting is safe: the hidden-service
// keys persist in their directories, so the onion addresses stay the
// same.
//
// In single-hop mode the client-only instance is started as well (after
// the service instance bootstrapped) - its own bootstrap is waited for
// too, so callers can rely on the client SOCKS port being usable when
// Start returns.
func (tm *TorManager) Start(ctx context.Context) error {
        if err := tm.GenerateTorrc(); err != nil {
                tm.setConnectFailed(err)
                return err
        }
        if tm.Running() {
                tm.Log.Detail("Restarting Tor so the current configuration takes effect")
                if err := tm.Stop(); err != nil {
                        tm.setConnectFailed(err)
                        return fmt.Errorf("restarting Tor: %v", err)
                }
        }
        if err := tm.startServiceInstance(ctx); err != nil {
                return err
        }
        if err := tm.waitBootstrap(ctx); err != nil {
                return err
        }
        if tm.State.Settings().SingleHopOnion {
                if err := tm.startClientInstance(ctx); err != nil {
                        return err
                }
                if err := tm.waitClientBootstrap(ctx); err != nil {
                        // the service instance is what publishes the onion services;
                        // a failing client only breaks relay registration/probes,
                        // which retry on their own - report but do not fail the start
                        tm.Log.Detail("client Tor instance: " + err.Error())
                }
        }
        return nil
}

// startServiceInstance launches the onion-service Tor process (the only
// instance in anonymous 3-hop mode).
func (tm *TorManager) startServiceInstance(ctx context.Context) error {
        bin, err := tm.FindTor()
        if err != nil {
                tm.setConnectFailed(err)
                return err
        }
        // Tor logs to its notice file per torrc; append any stdout/stderr
        // noise to the same file via an inherited fd (no pipe to sxops).
        logf, err := os.OpenFile(tm.Paths.TorLogFile(), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
        if err != nil {
                return err
        }
        cmd := exec.Command(bin, "-f", tm.torrcPath())
        cmd.Stdout = logf
        cmd.Stderr = logf
        prepareProcess(cmd)
        if err := cmd.Start(); err != nil {
                logf.Close()
                tm.setConnectFailed(err)
                return err
        }
        logf.Close()
        tm.mu.Lock()
        tm.cmd = cmd
        tm.exited = false
        stopCh := make(chan struct{})
        tm.stopCh = stopCh
        tm.stopping = false
        tm.startedAt = time.Now()
        tm.mu.Unlock()
        go func() {
                _ = cmd.Wait()
                tm.mu.Lock()
                tm.exited = true
                stopping := tm.stopping
                tm.mu.Unlock()
                if !stopping {
                        tm.mu.Lock()
                        restarts := tm.restarts
                        tm.mu.Unlock()
                        if restarts >= maxTorRestarts {
                                // A Tor that keeps dying (bad config, occupied ports) would
                                // flap forever - and the Tor panel would sit on "connecting".
                                // Give up and surface the failure instead.
                                tm.setConnectFailed(fmt.Errorf("Tor exited %d times in a row - it is not retried automatically:\n%s", restarts+1, tm.logTail()))
                                close(stopCh)
                                return
                        }
                        time.Sleep(2 * time.Second)
                        tm.mu.Lock()
                        stopping = tm.stopping
                        tm.mu.Unlock()
                        if ctx.Err() == nil && !stopping {
                                tm.mu.Lock()
                                tm.restarts++
                                n := tm.restarts
                                tm.mu.Unlock()
                                tm.Log.Detail(fmt.Sprintf("Tor exited unexpectedly - relaunching (attempt %d of %d)", n, maxTorRestarts))
                                if err := tm.Start(context.Background()); err != nil {
                                        tm.Log.Detail("Tor auto-relaunch failed: " + err.Error())
                                }
                        }
                }
                close(stopCh)
        }()
        return nil
}

// startClientInstance launches the client-only Tor instance (single-hop
// mode). It is started AFTER the service instance bootstrapped; a death
// is not auto-relaunched (the service instance restart heals it: Start
// starts both).
func (tm *TorManager) startClientInstance(ctx context.Context) error {
        if _, err := os.Stat(tm.Paths.TorClientRcPath()); err != nil {
                return nil // no client torrc - not in single-hop mode
        }
        if tm.clientRunning() {
                return nil
        }
        bin, err := tm.FindTor()
        if err != nil {
                return err
        }
        logf, err := os.OpenFile(tm.Paths.TorClientLogFile(), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
        if err != nil {
                return err
        }
        cmd := exec.Command(bin, "-f", tm.Paths.TorClientRcPath())
        cmd.Stdout = logf
        cmd.Stderr = logf
        prepareProcess(cmd)
        if err := cmd.Start(); err != nil {
                logf.Close()
                return err
        }
        logf.Close()
        tm.mu.Lock()
        tm.clientCmd = cmd
        tm.clientExited = false
        clientStopCh := make(chan struct{})
        tm.clientStopCh = clientStopCh
        tm.clientStopping = false
        tm.mu.Unlock()
        go func() {
                _ = cmd.Wait()
                tm.mu.Lock()
                tm.clientExited = true
                tm.mu.Unlock()
                close(clientStopCh)
        }()
        return nil
}

// clientRunning reports whether the client-only instance process is up
// (ours or adopted via its pidfile).
func (tm *TorManager) clientRunning() bool {
        tm.mu.Lock()
        cmd := tm.clientCmd
        exited := tm.clientExited
        tm.mu.Unlock()
        if cmd != nil && cmd.Process != nil && !exited {
                return true
        }
        if pid, ok := readPidFile(tm.Paths.TorClientPidFile()); ok && pidAlive(pid) {
                return true
        }
        return false
}

// waitBootstrap blocks until the control port reports 100% progress.
// The generous timeout matters: a first-ever bootstrap (or one through
// bridges) can take several minutes on slow or censored networks. A Tor
// that dies (bad config, port conflict) is reported quickly with the
// last log lines instead of wasting the whole window.
//
// v1.6.35: with bridges configured, a bootstrap that never gets past
// 25% while the log accumulates bridge connection failures is aborted
// early with a clear diagnosis instead of silently sitting on "25%"
// for the full five minutes - dead bridge lines are by far the most
// common cause of that stall (verified live: working bridges pass the
// whole flow in under 20 seconds on the same tor 0.4.9).
func (tm *TorManager) waitBootstrap(ctx context.Context) error {
        lastLogged := -1
        deadSince := time.Time{}
        started := time.Now()
        bridges := len(tm.State.Settings().TorBridges) > 0
        maxProgress := -1
        var diagErr error // set when the bridge diagnosis fired (v1.6.35)
        err := waitUntil(ctx, 5*time.Minute, 2*time.Second, func() (bool, error) {
                phase, ok := tm.BootstrapStatus()
                if ok {
                        deadSince = time.Time{}
                        if m := bootstrapProgressRe.FindStringSubmatch(phase); m != nil {
                                if p, perr := strconv.Atoi(m[1]); perr == nil {
                                        if p > maxProgress {
                                                maxProgress = p
                                        }
                                        // report progress at quartiles so the UI log shows movement
                                        for _, mark := range []int{25, 50, 75, 100} {
                                                if p >= mark && lastLogged < mark {
                                                        lastLogged = mark
                                                        tm.Log.Detail(fmt.Sprintf("Tor bootstrap progress: %d%%", p))
                                                }
                                        }
                                }
                        }
                        // v1.6.35: bridges that never connect - tor retries them in the
                        // background while the bootstrap sits at "asking for networkstatus
                        // consensus" (25%); the warn lines in the notice log prove it.
                        if bridges && !TorBootstrapped(phase) && maxProgress <= 25 &&
                                time.Since(started) > bridgeFailFastAfter {
                                if n := bridgeFailureCount(tm.readTorLog()); n >= bridgeFailFastAttempts {
                                        diagErr = tm.bridgeDiagnosisError(n)
                                        return false, diagErr
                                }
                        }
                        return ok && TorBootstrapped(phase), nil
                }
                // control port silent: fail fast when our Tor process died
                if tm.childDied() {
                        if deadSince.IsZero() {
                                deadSince = time.Now()
                        }
                        // the crash-restart goroutine relaunches after 2s;
                        // give it a few attempts before giving up
                        if time.Since(deadSince) > 15*time.Second {
                                return false, fmt.Errorf("Tor exited:\n%s", tm.logTail())
                        }
                }
                return false, nil
        })
        if err != nil {
                // v1.6.37: the caller aborted the connect (the Tor panel's
                // Stop button) or the caller's own deadline passed. A
                // deliberate abort is not a connect failure - the same rule
                // as Stop() - so the state stays clean; the caller decides
                // what to do with the still-running process (the UI stops
                // it, because a cancelled bootstrap leaves it alive).
                if cerr := ctx.Err(); cerr != nil {
                        return cerr
                }
                if diagErr != nil && err == diagErr {
                        // the bridge diagnosis: return it verbatim - it already says
                        // what is wrong and what to do about it
                        tm.setConnectFailed(err)
                        return err
                }
                if strings.HasPrefix(err.Error(), "Tor exited") {
                        tm.setConnectFailed(err)
                        return err
                }
                hint := tm.clockSkewHint()
                msg := "Tor did not connect within 5 minutes"
                if bridges {
                        n := bridgeFailureCount(tm.readTorLog())
                        msg = fmt.Sprintf("Tor did not connect within 5 minutes through the configured bridges - %d failed bridge connections are logged. The bridge lines may be offline, outdated or blocked by this network; get fresh ones from https://bridges.torproject.org", n)
                }
                if hint != "" {
                        msg += "\n\n" + hint
                }
                if tail := tm.logTail(); tail != "" {
                        msg += "\n\nLast Tor log lines:\n" + tail
                }
                tm.setConnectFailed(fmt.Errorf("%s", msg))
                return fmt.Errorf("%s", msg)
        }
        tm.Log.Detail("Tor bootstrapped 100%")
        tm.setConnectSucceeded()
        tm.mu.Lock()
        tm.restarts = 0 // the process is healthy again
        tm.mu.Unlock()
        _ = tm.State.With(func(st *State) { st.Tor.OnionReady = true })
        return nil
}

// waitClientBootstrap waits for the client-only instance to reach 100%
// (single-hop mode). Shorter budget than the service instance: it has no
// hidden services to publish, so a bootstrap is all it does.
func (tm *TorManager) waitClientBootstrap(ctx context.Context) error {
        tor := tm.State.Get().Tor
        if tor.ClientControlPort == 0 {
                return nil
        }
        lastLogged := -1
        return waitUntil(ctx, 3*time.Minute, 2*time.Second, func() (bool, error) {
                phase, ok := tm.bootstrapStatusPort(tor.ClientControlPort)
                if ok {
                        if m := bootstrapProgressRe.FindStringSubmatch(phase); m != nil {
                                if p, perr := strconv.Atoi(m[1]); perr == nil && p >= 50 && lastLogged < 50 {
                                        lastLogged = 50
                                        tm.Log.Detail("client Tor instance bootstrapping…")
                                }
                        }
                        return ok && TorBootstrapped(phase), nil
                }
                return false, nil
        })
}

// setConnectFailed records that the last connect attempt failed (drives
// the Tor panel's "Retry connection" vs "Connect" button label).
func (tm *TorManager) setConnectFailed(err error) {
        msg := ""
        if err != nil {
                msg = err.Error()
        }
        _ = tm.State.With(func(st *State) {
                st.Tor.ConnectFailed = true
                st.Tor.ConnectError = msg
        })
}

// setConnectSucceeded clears the failure marker after a successful
// bootstrap.
func (tm *TorManager) setConnectSucceeded() {
        _ = tm.State.With(func(st *State) {
                if st.Tor.ConnectFailed || st.Tor.ConnectError != "" {
                        st.Tor.ConnectFailed = false
                        st.Tor.ConnectError = ""
                }
        })
}

// childDied reports whether the Tor process launched by this sxops run
// (not an adopted one) has exited.
func (tm *TorManager) childDied() bool {
        tm.mu.Lock()
        defer tm.mu.Unlock()
        return tm.cmd != nil && tm.exited && !tm.stopping
}

// logTail returns the last lines of the Tor notice log for error
// messages (empty when there is nothing yet).
func (tm *TorManager) logTail() string {
        out, err := tailFile(tm.Paths.TorLogFile(), 15)
        if err != nil {
                return ""
        }
        return strings.TrimSpace(out)
}

var bootstrapProgressRe = regexp.MustCompile(`PROGRESS=(\d+)`)

// v1.6.35 bridge-stall diagnosis knobs (vars so tests can shorten them).
// Tor retries dead bridges roughly every 15-20 seconds, so six logged
// failures mean about two minutes of a certain stall; 45 seconds is the
// minimum runway before the verdict is trusted at all (a healthy bridge
// bootstrap clears the low phases in seconds).
var (
        bridgeFailFastAttempts = 6
        bridgeFailFastAfter    = 45 * time.Second
)

// bridgeAddr and the TCP preflight check that lived here were removed
// in v1.6.37 at the operator's request: the live-verified diagnosis
// (bridgeFailureCount + the fail-fast verdict below) covers the stalled
// bridge case, and the pre-connect TCP probe could reject bridges that
// censoring middleboxes would let through only after the transport
// handshake - it predicted less than it promised.

// bridgeFailureCount counts the warn signatures Tor writes while its
// bridge connections keep failing: "Proxy Client: unable to connect OR
// connection" (pluggable transports, verified against tor 0.4.9) and
// "Connection refused" (dead vanilla bridge endpoints).
func bridgeFailureCount(torLog string) int {
        s := strings.ToLower(torLog)
        return strings.Count(s, "unable to connect or connection") +
                strings.Count(s, "connection refused")
}

// readTorLog returns the whole Tor notice log ("" on any error) - used
// by the bridge diagnosis counters, which need the full history, not
// just the tail.
func (tm *TorManager) readTorLog() string {
        data, err := os.ReadFile(tm.Paths.TorLogFile())
        if err != nil {
                return ""
        }
        return string(data)
}

// bridgeDiagnosisError is the v1.6.35 verdict for bridges that never
// connect: what happened, why, and the way out.
func (tm *TorManager) bridgeDiagnosisError(attempts int) error {
        msg := fmt.Sprintf("The configured bridges are not connecting - Tor tried them %d times without success, so it cannot fetch the network data through them. The bridge lines are probably offline, outdated or blocked by this network.\n\nGet fresh bridge lines from https://bridges.torproject.org (or email bridges@torproject.org with the text: get transport obfs4), paste them into the bridges field and apply again.", attempts)
        if tail := tm.logTail(); tail != "" {
                msg += "\n\nLast Tor log lines:\n" + tail
        }
        return fmt.Errorf("%s", msg)
}

// clockSkewHint scans the Tor notice log for clock-related warnings - the
// number one cause of "Tor never connects" on WSL2, where the VM clock
// drifts after the Windows host sleeps or hibernates. Tor refuses to
// bootstrap when the clock is off by more than about an hour.
func (tm *TorManager) clockSkewHint() string {
        data, err := os.ReadFile(tm.Paths.TorLogFile())
        if err != nil {
                return ""
        }
        s := strings.ToLower(string(data))
        clockIssue := strings.Contains(s, "our clock") ||
                strings.Contains(s, "system clock") ||
                strings.Contains(s, "stale consensus") ||
                strings.Contains(s, "not enough time") ||
                strings.Contains(s, "before the earliest valid consensus")
        if !clockIssue {
                return ""
        }
        if InWSL() {
                return "Your Linux clock is wrong (a common WSL problem after the PC slept).\n" +
                        "Fix: open a Windows terminal, run  wsl --shutdown , open Ubuntu again\n" +
                        "(this re-syncs the clock), then press Retry connection here.\n" +
                        "Alternatively run  sudo hwclock -s  in the Ubuntu terminal."
        }
        return "Your computer's clock seems wrong - Tor refuses to connect when it is off.\n" +
                "Fix: sync it (e.g.  sudo hwclock -s  or enable automatic time), then press Retry connection."
}

// InWSL reports whether sxops runs inside the Windows Subsystem for Linux.
func InWSL() bool {
        data, err := os.ReadFile("/proc/version")
        if err != nil {
                return false
        }
        s := strings.ToLower(string(data))
        return strings.Contains(s, "microsoft") || strings.Contains(s, "wsl")
}

// Running reports whether the Tor process is up - ours, or an adopted
// instance from a previous sxops run (detected via the control port or
// pidfile).
func (tm *TorManager) Running() bool {
        tm.mu.Lock()
        cmd := tm.cmd
        exited := tm.exited
        tm.mu.Unlock()
        if cmd != nil && cmd.Process != nil && !exited {
                return true
        }
        // adopted: control port answers?
        if _, ok := tm.BootstrapStatus(); ok {
                return true
        }
        // adopted: pidfile alive but control port not up yet?
        if pid, ok := readPidFile(tm.Paths.TorPidFile()); ok && pidAlive(pid) {
                return true
        }
        return false
}

// Status snapshots the Tor instance.
func (tm *TorManager) Status() TorStatus {
        st := TorStatus{Started: tm.startedAt}
        tor := tm.State.Get().Tor
        st.SocksPort = tor.SocksPort
        st.ControlPort = tor.ControlPort
        st.ClientSocksPort = ClientSocksPortOf(tm.State)
        if tm.Running() {
                st.Running = true
                tm.mu.Lock()
                if tm.cmd != nil && tm.cmd.Process != nil && !tm.exited {
                        st.PID = tm.cmd.Process.Pid
                }
                tm.mu.Unlock()
                if st.PID == 0 {
                        if pid, ok := readPidFile(tm.Paths.TorPidFile()); ok {
                                st.PID = pid
                        }
                }
                if phase, ok := tm.BootstrapStatus(); ok {
                        st.BootstrapMsg = phase
                        st.Bootstrapped = TorBootstrapped(phase)
                }
        }
        return st
}

// Stop terminates Tor (ours or an adopted instance) - the client-only
// instance first (single-hop mode), then the service instance.
func (tm *TorManager) Stop() error {
        tm.stopClient()
        tm.mu.Lock()
        cmd := tm.cmd
        stopCh := tm.stopCh
        tm.stopping = true
        tm.mu.Unlock()
        if cmd == nil || cmd.Process == nil {
                return tm.stopAdopted()
        }
        interruptProcess(cmd.Process)
        ch := make(chan struct{})
        if stopCh != nil {
                go func() {
                        <-stopCh
                        close(ch)
                }()
        } else {
                close(ch)
        }
        select {
        case <-ch:
        case <-time.After(10 * time.Second):
                _ = cmd.Process.Kill()
                <-ch
        }
        tm.mu.Lock()
        tm.cmd = nil
        tm.restarts = 0
        tm.mu.Unlock()
        _ = os.Remove(tm.Paths.TorPidFile())
        _ = tm.State.With(func(st *State) {
                st.Tor.OnionReady = false
                // a deliberate stop is not a connect failure - the panel
                // offers "Connect", not "Retry connection"
                st.Tor.ConnectFailed = false
                st.Tor.ConnectError = ""
        })
        return nil
}

// stopClient stops the client-only instance (single-hop mode).
func (tm *TorManager) stopClient() {
        tm.mu.Lock()
        cmd := tm.clientCmd
        stopCh := tm.clientStopCh
        tm.mu.Unlock()
        if cmd == nil || cmd.Process == nil {
                // adopted client instance from a previous sxops run
                if pid, ok := readPidFile(tm.Paths.TorClientPidFile()); ok && pidAlive(pid) {
                        killPid(pid)
                }
                _ = os.Remove(tm.Paths.TorClientPidFile())
                tm.mu.Lock()
                tm.clientCmd = nil
                tm.mu.Unlock()
                return
        }
        tm.mu.Lock()
        tm.clientStopping = true
        tm.mu.Unlock()
        interruptProcess(cmd.Process)
        if stopCh != nil {
                select {
                case <-stopCh:
                case <-time.After(10 * time.Second):
                        _ = cmd.Process.Kill()
                        <-stopCh
                }
        } else {
                _ = cmd.Process.Kill()
        }
        tm.mu.Lock()
        tm.clientCmd = nil
        tm.mu.Unlock()
        _ = os.Remove(tm.Paths.TorClientPidFile())
}

// purgeDirectoryCache drops Tor's cached directory documents and guard
// state from both DataDirectories (the service instance's and, in
// single-hop mode, the client instance's). Tor reuses that cache across
// restarts: a still-fresh cached consensus is NOT refetched, so after a
// bridge change the bootstrap runs on documents obtained over the
// PREVIOUS network path - progress jumps ahead on the stale consensus
// and then stalls at 25% ("asking for networkstatus consensus" on
// current Tor) when the fetch through the new bridges does not finish,
// which users read as "the bridges are broken". A clean cache forces
// every document through the NEW bridges. This is also the standard
// remedy for bridges that stopped working ("clear the cached
// consensus").
//
// Only Tor's own cache files are removed (cached-*, state, lock - plus
// temporary siblings); directories such as pt_state/ are left alone, and
// the hidden-service keys live in a separate tree (tor/hs), so the onion
// addresses are unchanged.
func (tm *TorManager) purgeDirectoryCache() {
        for _, dir := range []string{tm.Paths.TorDataDir(), tm.Paths.TorClientDataDir()} {
                entries, err := os.ReadDir(dir)
                if err != nil {
                        continue // no datadir yet - nothing cached
                }
                for _, en := range entries {
                        if en.IsDir() {
                                continue
                        }
                        name := en.Name()
                        if strings.HasPrefix(name, "cached-") ||
                                name == "state" || strings.HasPrefix(name, "state.") ||
                                name == "lock" {
                                _ = os.Remove(filepath.Join(dir, name))
                        }
                }
        }
}

// ResetHiddenServices removes the hidden-service directories so fresh
// onion keys (and addresses) are generated on the next Tor start.
//
// Tor forbids re-using a hidden service directory that has been used in
// single-hop (non-anonymous) mode for an anonymous hidden service - and
// the reverse - so EVERY change of the single-hop setting requires this
// regeneration. The onion addresses change with it (unavoidable); the
// server INIs are re-patched by the engine so they advertise only the
// still-valid hosts until the new onions publish.
func (tm *TorManager) ResetHiddenServices() {
        for _, k := range hiddenServiceKinds() {
                _ = os.RemoveAll(tm.Paths.HiddenServiceDir(k))
                _ = os.MkdirAll(tm.Paths.HiddenServiceDir(k), 0o700)
                _ = tm.State.UpdateServer(k, func(r *ServerRec) { r.OnionHost = "" })
        }
        _ = tm.State.With(func(st *State) { st.Tor.OnionReady = false })
}

// stopAdopted stops a Tor instance started by a previous sxops run,
// preferring a clean SIGNAL SHUTDOWN via the control port.
func (tm *TorManager) stopAdopted() error {
        pid, ok := readPidFile(tm.Paths.TorPidFile())
        if !ok && !tm.Running() {
                return nil
        }
        if ok && !pidAlive(pid) {
                _ = os.Remove(tm.Paths.TorPidFile())
                return nil
        }
        // clean shutdown via control port
        if tm.controlShutdown() {
                deadline := time.Now().Add(8 * time.Second)
                for time.Now().Before(deadline) {
                        if ok && !pidAlive(pid) {
                                _ = os.Remove(tm.Paths.TorPidFile())
                                return nil
                        }
                        if !ok && !tm.Running() {
                                return nil
                        }
                        time.Sleep(300 * time.Millisecond)
                }
        }
        if ok {
                killPid(pid)
                _ = os.Remove(tm.Paths.TorPidFile())
        }
        _ = tm.State.With(func(st *State) { st.Tor.OnionReady = false })
        return nil
}

// controlShutdown sends SIGNAL SHUTDOWN on the Tor control port.
func (tm *TorManager) controlShutdown() bool {
        tor := tm.State.Get().Tor
        if tor.ControlPort == 0 {
                return false
        }
        conn, err := net.DialTimeout("tcp", "127.0.0.1:"+strconv.Itoa(tor.ControlPort), 2*time.Second)
        if err != nil {
                return false
        }
        defer conn.Close()
        conn.SetDeadline(time.Now().Add(3 * time.Second))
        if _, err := conn.Write([]byte("PROTOCOLINFO 1\r\n")); err != nil {
                return false
        }
        cookiePath := readProtocolInfoCookiePath(conn)
        if cookiePath == "" {
                return false
        }
        cookie, err := os.ReadFile(cookiePath)
        if err != nil {
                return false
        }
        if _, err := conn.Write([]byte("AUTHENTICATE " + hex.EncodeToString(cookie) + "\r\n")); err != nil {
                return false
        }
        if resp := readUntilDone(conn); !strings.Contains(resp, "250 OK") {
                return false
        }
        if _, err := conn.Write([]byte("SIGNAL SHUTDOWN\r\n")); err != nil {
                return false
        }
        return strings.Contains(readUntilDone(conn), "250 OK")
}

// TorBootstrapped reports whether a BootstrapStatus phase string says
// the bootstrap is complete. tor's reply is "PROGRESS=100 TAG=Done"
// (no percent sign anywhere), and BootstrapStatus forwards it as
// PROGRESS=100 SUMMARY="Done".
func TorBootstrapped(phase string) bool {
        return strings.Contains(phase, "PROGRESS=100")
}

// BootstrapStatus queries the Tor control port (cookie auth) for
// status/bootstrap-phase.
func (tm *TorManager) BootstrapStatus() (string, bool) {
        tor := tm.State.Get().Tor
        if tor.ControlPort == 0 {
                return "", false
        }
        return tm.bootstrapStatusPort(tor.ControlPort)
}

// bootstrapStatusPort queries status/bootstrap-phase on one control port.
func (tm *TorManager) bootstrapStatusPort(ctrlPort int) (string, bool) {
        if ctrlPort == 0 {
                return "", false
        }
        conn, err := net.DialTimeout("tcp", "127.0.0.1:"+strconv.Itoa(ctrlPort), 2*time.Second)
        if err != nil {
                return "", false
        }
        defer conn.Close()
        // PROTOCOLINFO to obtain the cookie file location
        conn.SetDeadline(time.Now().Add(3 * time.Second))
        if _, err := conn.Write([]byte("PROTOCOLINFO 1\r\n")); err != nil {
                return "", false
        }
        cookiePath := readProtocolInfoCookiePath(conn)
        if cookiePath == "" {
                return "", false
        }
        cookie, err := os.ReadFile(cookiePath)
        if err != nil {
                return "", false
        }
        if _, err := conn.Write([]byte("AUTHENTICATE " + hex.EncodeToString(cookie) + "\r\n")); err != nil {
                return "", false
        }
        resp := readUntilDone(conn)
        if !strings.Contains(resp, "250 OK") {
                return "", false
        }
        if _, err := conn.Write([]byte("GETINFO status/bootstrap-phase\r\n")); err != nil {
                return "", false
        }
        out := readUntilDone(conn)
        // tor replies "250-status/bootstrap-phase=BOOTSTRAP PROGRESS=100 TAG=Done SUMMARY=\"Done\"";
        // [^"]* crosses the TAG=... field that [^=]* never could
        m := regexp.MustCompile(`BOOTSTRAP PROGRESS=(\d+)[^"]*SUMMARY="([^"]+)"`)
        if mm := m.FindStringSubmatch(out); mm != nil {
                return fmt.Sprintf("PROGRESS=%s SUMMARY=%q", mm[1], mm[2]), true
        }
        return "", false
}

func readProtocolInfoCookiePath(conn net.Conn) string {
        conn.SetReadDeadline(time.Now().Add(2 * time.Second))
        var sb strings.Builder
        buf := make([]byte, 4096)
        for {
                n, err := conn.Read(buf)
                if n > 0 {
                        sb.Write(buf[:n])
                }
                s := sb.String()
                if strings.Contains(s, "250 OK") || strings.Contains(s, "250 ") && strings.Count(s, "250 ") > 1 {
                        break
                }
                if err != nil {
                        break
                }
        }
        // tor replies "250-AUTH METHODS=COOKIE COOKIEFILE=\"/path\"" - not
        // "AUTH COOKIE FILE=", so match the real keyword
        m := regexp.MustCompile(`COOKIEFILE="([^"]+)"`).FindStringSubmatch(sb.String())
        if m == nil {
                return ""
        }
        return strings.ReplaceAll(m[1], "\\\"", "\"")
}

func readUntilDone(conn net.Conn) string {
        conn.SetReadDeadline(time.Now().Add(3 * time.Second))
        var sb strings.Builder
        buf := make([]byte, 4096)
        for {
                n, err := conn.Read(buf)
                if n > 0 {
                        sb.Write(buf[:n])
                }
                s := sb.String()
                if strings.Contains(s, "250 OK\r\n.\r\n") || strings.HasSuffix(s, "250 OK\r\n") {
                        break
                }
                if err != nil {
                        break
                }
        }
        return sb.String()
}

// OnionHost reads the hidden service hostname for a kind.
func (tm *TorManager) OnionHost(k Kind) (string, error) {
        data, err := os.ReadFile(filepath.Join(tm.Paths.HiddenServiceDir(k), "hostname"))
        if err != nil {
                return "", err
        }
        h := strings.TrimSpace(string(data))
        if h == "" || !strings.HasSuffix(h, ".onion") {
                return "", fmt.Errorf("onion hostname not ready")
        }
        return h, nil
}

// WaitForOnions waits for all enabled hidden services to publish.
func (tm *TorManager) WaitForOnions(ctx context.Context) (map[Kind]string, error) {
        out := map[Kind]string{}
        err := waitUntil(ctx, 90*time.Second, 2*time.Second, func() (bool, error) {
                out = map[Kind]string{}
                ready := true
                for _, k := range hiddenServiceKinds() {
                        rec := tm.State.Server(k)
                        if !rec.Installed {
                                continue
                        }
                        h, err := tm.OnionHost(k)
                        if err != nil {
                                ready = false
                        } else {
                                out[k] = h
                        }
                }
                return ready, nil
        })
        return out, err
}

// probeTorSOCKS opens a SOCKS5 CONNECT through the managed Tor instance
// to host:port and reports whether the connection succeeded. For onion
// addresses this proves the full chain: descriptor fetched, rendezvous
// circuit established and the hidden service accepted the stream. The
// connection is closed immediately after (a bare TCP connect is enough -
// the SMP server waits for the transport handshake).
func probeTorSOCKS(socksPort int, host string, port int) error {
        // 40s: onion rendezvous can legitimately take tens of seconds on a
        // slow Tor network (the simplex-chat CLI uses --tcp-timeout 45 for
        // the same reason) - a shorter deadline reported working servers as
        // "not reachable" while the app-style tests passed.
        return probeTorSOCKSTimeout(socksPort, host, port, 40*time.Second)
}

// probeTorSOCKSTimeout is probeTorSOCKS with a custom per-probe deadline.
// Onion rendezvous can legitimately take several seconds on slow networks,
// so the deadline must stay generous; short deadlines are only used by
// retry loops (waitOnionReachable) that probe again quickly.
func probeTorSOCKSTimeout(socksPort int, host string, port int, deadline time.Duration) error {
        addr := net.JoinHostPort("127.0.0.1", strconv.Itoa(socksPort))
        conn, err := net.DialTimeout("tcp", addr, 5*time.Second)
        if err != nil {
                return fmt.Errorf("connecting to the Tor SOCKS port: %v", err)
        }
        defer conn.Close()
        conn.SetDeadline(time.Now().Add(deadline))
        // SOCKS5 greeting: no authentication
        if _, err := conn.Write([]byte{0x05, 0x01, 0x00}); err != nil {
                return fmt.Errorf("SOCKS greeting: %v", err)
        }
        resp := make([]byte, 2)
        if _, err := io.ReadFull(conn, resp); err != nil {
                return fmt.Errorf("SOCKS greeting reply: %v", err)
        }
        if resp[0] != 0x05 || resp[1] != 0x00 {
                return fmt.Errorf("SOCKS greeting refused (reply %v)", resp)
        }
        // SOCKS5 CONNECT with the onion hostname (ATYP=0x03 domain name)
        req := []byte{0x05, 0x01, 0x00, 0x03, byte(len(host))}
        req = append(req, []byte(host)...)
        req = append(req, byte(port>>8), byte(port&0xff))
        if _, err := conn.Write(req); err != nil {
                return fmt.Errorf("SOCKS connect: %v", err)
        }
        head := make([]byte, 4)
        if _, err := io.ReadFull(conn, head); err != nil {
                return fmt.Errorf("SOCKS connect reply: %v", err)
        }
        if head[1] != 0x00 {
                return fmt.Errorf("Tor could not reach %s:%d (SOCKS error 0x%02x)", host, port, head[1])
        }
        // success: drain the bound address to keep the protocol clean
        rest := make([]byte, 2)
        if _, err := io.ReadFull(conn, rest); err == nil {
                l := 0
                switch head[3] {
                case 0x01:
                        l = 4
                case 0x03:
                        l = int(rest[1]) + 2
                case 0x04:
                        l = 16
                }
                if l > 0 {
                        _, _ = io.ReadFull(conn, make([]byte, l))
                }
        }
        return nil
}
