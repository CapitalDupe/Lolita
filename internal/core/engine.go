package core

import (
        "context"
        "fmt"
        "os"
        "path/filepath"
        "strconv"
        "strings"
        "sync"
        "time"
)

// Backend abstracts where server processes run. sxops runs as a native
// Linux application; on Windows it runs the whole stack inside WSL2
// (one-time manual setup, see README), where the same native backend is used.
type Backend interface {
        Name() string
        InstallBinary(ctx context.Context, k Kind, rel *Release) error
        InitServer(k Kind, args []string) (string, error)
        StartServer(k Kind) error
        StopServer(k Kind) error
        ServerRunning(k Kind) bool
        ReadServerFile(k Kind, relPath string) ([]byte, error)
        WriteServerFile(k Kind, relPath string, data []byte) error
        ServerLogTail(k Kind, n int) ([]byte, error)
        WriteTorrc(content string) error
        StartTor(ctx context.Context) error
        StopTor() error
        TorRunning() bool
        BootstrapStatus() (string, bool)
        ReadOnionHost(k Kind) (string, error)
        NeedsTorInstall() (bool, string)
        // InstallTor installs Tor when the backend can do it for the user.
        // The native Linux backend returns an error with copy-able
        // instructions for the package manager.
        InstallTor(ctx context.Context) error
        // TorLogTail returns the last lines of the Tor notice log.
        TorLogTail(lines int) ([]byte, error)
        // ReadStoreLog returns the tail of the server's store log (the
        // queue/file journal used for moderation).
        ReadStoreLog(k Kind, maxLines int) ([]byte, error)
}

// Engine is the facade used by the GUI and CLI.
type Engine struct {
        Paths       *Paths
        State       *StateStore
        Sv          *Supervisor
        Tor         *TorManager
        Installer   *Installer
        Provisioner *Provisioner
        Log         InstallLog
        Backend     Backend
}

// NewEngine wires everything together for the current platform.
func NewEngine(log InstallLog) (*Engine, error) {
        paths, err := NewPaths("")
        if err != nil {
                return nil, err
        }
        if err := paths.Ensure(); err != nil {
                return nil, err
        }
        state, err := NewStateStore(paths)
        if err != nil {
                return nil, err
        }
        if log == nil {
                log = NopInstallLog()
        }
        sv := NewSupervisor(paths, state)
        eng := &Engine{
                Paths:       paths,
                State:       state,
                Sv:          sv,
                Tor:         NewTorManager(paths, state, log),
                Installer:   NewInstaller(paths, state, log),
                Provisioner: NewProvisioner(paths, state, log),
                Log:         log,
        }
        eng.Backend = newPlatformBackend(eng)
        sv.PreStart = eng.preStartServer
        // v1.5.8: nothing starts automatically anymore - unregister the
        // login autostart older versions may have left behind.
        removeLegacyAutostart(log)
        return eng, nil
}

// EnsurePortlessLinks is the one-time v1.6.3 upgrade: torrcs from older
// sxops map only the canonical virtual ports (5223/5225/5227). The
// portless onion addresses v1.6.3 renders rely on the clients' protocol
// defaults (SMP 5223, XFTP/NTF 443 - see clientDefaultPort) being mapped
// as virtual ports too. When the on-disk torrc lacks them it is
// regenerated; a RUNNING Tor is restarted once - a running Tor never
// re-reads its torrc - and that is safe: the hidden-service keys persist
// in their directories, so the onion addresses stay the same. Old
// addresses with an explicit port keep working (the canonical virtual
// ports stay mapped).
//
// It is synchronous and idempotent (a no-op once the layout is current):
// ensureTorRunning calls it before every start flow, and `sxops check` /
// `sxops addresses` call it so the links they verify or print are LIVE.
// A background goroutine would not work here - one-shot CLI commands
// exit before it finishes.
func (e *Engine) EnsurePortlessLinks() {
        st := e.State.Get()
        if !st.Tor.Managed {
                return
        }
        installed := false
        for _, k := range hiddenServiceKinds() {
                if rec := st.Servers[k]; rec != nil && rec.Installed {
                        installed = true
                        break
                }
        }
        if !installed || e.Tor.torrcHasDefaultPorts() {
                return
        }
        if !e.Tor.Running() {
                // Tor is down: write the new layout now; the next start uses it.
                if err := e.Tor.GenerateTorrc(); err != nil {
                        e.Log.Detail("portless-links migration: " + err.Error())
                }
                return
        }
        e.Log.Detail("restarting Tor once - adding the default virtual ports (5223/443) the portless onion addresses rely on (the onion addresses themselves do not change)")
        ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
        defer cancel()
        if err := e.Tor.Start(ctx); err != nil {
                e.Log.Detail("portless-links migration: " + err.Error())
        }
}

// UpdateInfo describes the update state of one managed component.
type UpdateInfo struct {
        Kind      Kind
        Name      string
        Installed bool
        Current   string // installed release tag ("unknown" when never checked)
        Latest    string // latest release tag from GitHub
        Available bool
        Err       string // check error (shown in the UI)
}

// CheckUpdates queries GitHub for the latest release of every installed
// component and reports whether an update is available. The GitHub token
// from settings is used when configured.
func (e *Engine) CheckUpdates(ctx context.Context) []UpdateInfo {
        var out []UpdateInfo
        for _, k := range []Kind{KindSMP, KindXFTP, KindNTF, KindRelay} {
                rec := e.State.Server(k)
                info := UpdateInfo{Kind: k, Name: Def(k).Name, Installed: rec.Installed, Current: rec.BinTag}
                if !rec.Installed {
                        out = append(out, info)
                        continue
                }
                rel, upd, err := e.Installer.CheckUpdate(ctx, k)
                switch {
                case err != nil:
                        info.Err = err.Error()
                case upd:
                        info.Latest = rel.TagName
                        info.Available = true
                default:
                        info.Latest = rel.TagName
                }
                out = append(out, info)
        }
        return out
}

// UpdateComponent stops the component when it is running, installs the
// latest release binary and starts it again (with the correct arguments -
// the chat relay needs its full argv). It is a no-op when the component
// is already up to date; returns the tag it ended up on.
func (e *Engine) UpdateComponent(ctx context.Context, k Kind) (string, error) {
        rel, upd, err := e.Installer.CheckUpdate(ctx, k)
        if err != nil {
                return "", err
        }
        if !upd {
                return rel.TagName, nil // already current
        }
        wasRunning := e.Backend.ServerRunning(k)
        if wasRunning {
                e.Log.Step("Stopping " + Def(k).Name + " for the update")
                if err := e.Backend.StopServer(k); err != nil {
                        return "", fmt.Errorf("stopping %s: %v", Def(k).Name, err)
                }
        }
        if err := e.Backend.InstallBinary(ctx, k, rel); err != nil {
                // try to bring the old version back up before returning
                if wasRunning {
                        _ = e.startAfterUpdate(k)
                }
                return "", fmt.Errorf("%s: %v", Def(k).Name, err)
        }
        if wasRunning {
                e.Log.Step("Starting " + Def(k).Name)
                if err := e.startAfterUpdate(k); err != nil {
                        return rel.TagName, fmt.Errorf("%s started with the new version, but: %v", Def(k).Name, err)
                }
        }
        return rel.TagName, nil
}

// startAfterUpdate launches a component with the right argv for its kind.
// ensureTorRunning makes sure the managed Tor instance is up: when it
// is not running it is started now, blocking until it is bootstrapped.
// Servers start once Tor is running - there is no direct/no-Tor path.
func (e *Engine) ensureTorRunning(ctx context.Context) error {
        if e.Backend.TorRunning() {
                // v1.6.3: a running Tor must also run the CURRENT torrc
                // (a running Tor never re-reads it) - the one-time
                // portless-links migration restarts it when the layout
                // changed (the onion keys persist, addresses stay the same).
                e.EnsurePortlessLinks()
                // v1.6.29: one-time activation of the SMP server's HTTPS
                // link page (browsers opening a shared https:// link get
                // the invite page instead of a connection error).
                e.EnsureSMPWebPage()
                return nil
        }
        e.Log.Detail("starting Tor first - servers start once Tor is running")
        if err := e.Backend.StartTor(ctx); err != nil {
                return fmt.Errorf("Tor is not running and could not be started: %v", err)
        }
        return nil
}

// StartComponent starts one component (the GUI's per-component Start
// button). Servers and the chat relay only start once Tor is running:
// when Tor is down it is started first (blocking until bootstrapped).
func (e *Engine) StartComponent(k Kind) error {
        if k == KindRelay {
                return e.StartRelay()
        }
        if err := e.ensureTorRunning(context.Background()); err != nil {
                return err
        }
        return e.Backend.StartServer(k)
}

// startAfterUpdate restarts a component after its binary was updated.
func (e *Engine) startAfterUpdate(k Kind) error {
        return e.StartComponent(k)
}

// UpdateAll updates every installed component that has a newer release,
// stopping and restarting each in turn. Returns the number of updated
// components and the first error encountered (already-updated components
// are skipped, so one failure does not block the rest).
func (e *Engine) UpdateAll(ctx context.Context) (int, []string) {
        var problems []string
        n := 0
        for _, k := range []Kind{KindSMP, KindXFTP, KindNTF, KindRelay} {
                if !e.State.Server(k).Installed {
                        continue
                }
                _, upd, err := e.Installer.CheckUpdate(ctx, k)
                if err != nil {
                        problems = append(problems, fmt.Sprintf("%s: %v", Def(k).Name, err))
                        continue
                }
                if !upd {
                        continue
                }
                if _, err := e.UpdateComponent(ctx, k); err != nil {
                        problems = append(problems, err.Error())
                        continue
                }
                n++
        }
        return n, problems
}

// ---- native backend (Linux / native processes) ----

type nativeBackend struct{ eng *Engine }

func (n *nativeBackend) Name() string { return "native" }

func (n *nativeBackend) InstallBinary(ctx context.Context, k Kind, rel *Release) error {
        return n.eng.Installer.Install(ctx, k, rel)
}

func (n *nativeBackend) InitServer(k Kind, args []string) (string, error) {
        d := Def(k)
        env := []string{d.CfgEnvVar + "=" + n.eng.Paths.CfgDir(k), d.LogEnvVar + "=" + n.eng.Paths.LogDir(k)}
        return CommandOutput(n.eng.Paths.BinPath(k), args, env, 0)
}

func (n *nativeBackend) StartServer(k Kind) error {
        // port conflicts with other programs are resolved automatically:
        // the local listen port moves, the advertised address does not.
        return n.eng.ensurePortsAndStart(k)
}

func (n *nativeBackend) StopServer(k Kind) error {
        return n.eng.Sv.Stop(k, 20*time.Second)
}

func (n *nativeBackend) ServerRunning(k Kind) bool {
        st := n.eng.Sv.Status(k)
        return st.State == ProcRunning || st.State == ProcStarting
}

func (n *nativeBackend) ReadServerFile(k Kind, relPath string) ([]byte, error) {
        return os.ReadFile(filepath.Join(n.eng.Paths.CfgDir(k), relPath))
}

func (n *nativeBackend) WriteServerFile(k Kind, relPath string, data []byte) error {
        p := filepath.Join(n.eng.Paths.CfgDir(k), relPath)
        if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
                return err
        }
        return os.WriteFile(p, data, 0o600)
}

func (n *nativeBackend) ServerLogTail(k Kind, lines int) ([]byte, error) {
        out, err := tailFile(n.eng.Paths.ProcLogFile(k), lines)
        return []byte(out), err
}

// ReadStoreLog tails the server store log from the portable LOG dir.
func (n *nativeBackend) ReadStoreLog(k Kind, maxLines int) ([]byte, error) {
        if name := StoreLogName(k); name != "" {
                out, err := tailFile(filepath.Join(n.eng.Paths.LogDir(k), name), maxLines)
                return []byte(out), err
        }
        return nil, fmt.Errorf("%s has no store log", Def(k).Name)
}

func (n *nativeBackend) WriteTorrc(content string) error {
        // the TorManager generates it directly
        return nil
}

func (n *nativeBackend) StartTor(ctx context.Context) error {
        return n.eng.Tor.Start(ctx)
}

func (n *nativeBackend) StopTor() error { return n.eng.Tor.Stop() }

func (n *nativeBackend) TorRunning() bool { return n.eng.Tor.Running() }

func (n *nativeBackend) BootstrapStatus() (string, bool) { return n.eng.Tor.BootstrapStatus() }

func (n *nativeBackend) ReadOnionHost(k Kind) (string, error) { return n.eng.Tor.OnionHost(k) }

func (n *nativeBackend) NeedsTorInstall() (bool, string) {
        _, err := n.eng.Tor.FindTor()
        if err != nil {
                return true, err.Error()
        }
        return false, ""
}

func (n *nativeBackend) InstallTor(ctx context.Context) error {
        return fmt.Errorf("On Linux, Tor comes from your package manager.\n" +
                "Open a terminal and run:  sudo apt install tor\n" +
                "(or sudo dnf install tor / sudo pacman -S tor), then press Retry.")
}

func (n *nativeBackend) TorLogTail(lines int) ([]byte, error) {
        out, err := tailFile(n.eng.Paths.TorLogFile(), lines)
        return []byte(out), err
}

// ---- engine-level operations ----

// SetupOptions selects components for the guided setup.
type SetupOptions struct {
        EnableSMP   bool
        EnableXFTP  bool
        EnableNTF   bool
        EnableRelay bool
        // AllowTorFailure keeps setup going when Tor cannot bootstrap (e.g.
        // no internet yet): servers still start on localhost and the GUI can
        // retry onion publication later from the Tor panel.
        AllowTorFailure bool
}

// FullSetup performs the complete guided deployment:
// install binaries, init configs, patch INIs, start Tor, publish onion
// services, start servers, start the chat relay.
// It returns non-fatal warnings (e.g. Tor not connected yet).
func (e *Engine) FullSetup(ctx context.Context, opts SetupOptions) ([]string, error) {
        warnings := []string{}
        s := e.State.Settings()
        _ = s

        // 0. platform preparation (no-op on Linux)
        if err := e.ensureBackend(ctx); err != nil {
                return warnings, err
        }

        // 1. install binaries
        kinds := []Kind{}
        if opts.EnableSMP {
                kinds = append(kinds, KindSMP)
        }
        if opts.EnableXFTP {
                kinds = append(kinds, KindXFTP)
        }
        if opts.EnableNTF || s.EnableNTF {
                kinds = append(kinds, KindNTF)
                if !s.EnableNTF {
                        _ = e.State.With(func(st *State) { st.Settings.EnableNTF = true })
                }
        }
        if opts.EnableRelay {
                kinds = append(kinds, KindRelay)
        }
        // Components that were NOT selected (typically the optional NTF
        // server) are explicitly marked disabled, so the dashboard shows
        // them as "off" instead of looking like a broken install.
        for _, k := range []Kind{KindSMP, KindXFTP, KindNTF, KindRelay} {
                selected := false
                for _, kk := range kinds {
                        if kk == k {
                                selected = true
                                break
                        }
                }
                if !selected {
                        _ = e.State.UpdateServer(k, func(r *ServerRec) { r.Enabled = false })
                }
        }
        for _, k := range kinds {
                rec := e.State.Server(k)
                if rec.Installed {
                        continue
                }
                rel, _, err := e.Installer.CheckUpdate(ctx, k)
                if err != nil {
                        return warnings, fmt.Errorf("%s: %v", Def(k).Name, err)
                }
                if err := e.Backend.InstallBinary(ctx, k, rel); err != nil {
                        return warnings, fmt.Errorf("%s: %v", Def(k).Name, err)
                }
        }

        // The NTF server needs its database before it can start.
        if kindIn(KindNTF, kinds) && e.State.Settings().NTFDBConnection == "" {
                if err := e.ensureManagedPostgres(); err != nil {
                        return warnings, fmt.Errorf("notification server: %v", err)
                }
        }
        // 2. init servers (idempotent: init refuses when INI exists)
        for _, k := range kinds {
                if k == KindRelay {
                        if err := e.Provisioner.provisionRelay(); err != nil {
                                return warnings, err
                        }
                        _ = e.State.UpdateServer(KindRelay, func(r *ServerRec) {
                                r.Installed = true
                                r.Enabled = opts.EnableRelay
                        })
                        continue
                }
                rec := e.State.Server(k)
                if rec.Fingerprint != "" {
                        continue // already initialized
                }
                s := e.State.Settings()
                args, err := InitArgs(k, s, e.Paths)
                if err != nil {
                        return warnings, err
                }
                e.Log.Step("Initializing " + Def(k).Name)
                out, err := e.Backend.InitServer(k, args)
                if err != nil {
                        return warnings, fmt.Errorf("%s init: %v\n%s", Def(k).Name, err, truncate(out, 600))
                }
                // portable profile patches applied to the INI
                if err := e.patchServerIni(k); err != nil {
                        return warnings, fmt.Errorf("%s config: %v", Def(k).Name, err)
                }
                if err := e.refreshServerRecords(k); err != nil {
                        return warnings, err
                }
        }

        // 3. Tor + onion services
        e.Log.Step("Starting Tor and publishing onion services")

        // Resolve local port conflicts BEFORE generating the torrc: the
        // onion services must map their virtual ports to the local ports
        // the servers will actually bind (a foreign program may have
        // taken one of the canonical ports since the last run).
        for _, k := range kinds {
                if k == KindRelay {
                        continue
                }
                if moved, err := e.EnsureServerPorts(k); err != nil {
                        warnings = append(warnings, err.Error())
                } else {
                        for _, m := range moved {
                                e.Log.Step(m)
                        }
                }
        }

        torOK := true
        if needs, why := e.Backend.NeedsTorInstall(); needs {
                if opts.AllowTorFailure {
                        warnings = append(warnings, "Tor is not installed yet ("+why+") - servers will start, but onion addresses appear only after Tor is installed and connected. Use the Retry button on the Tor tab.")
                        torOK = false
                } else {
                        return warnings, fmt.Errorf("Tor: %s", why)
                }
        }
        if torOK {
                if err := e.Backend.StartTor(ctx); err != nil {
                        if opts.AllowTorFailure {
                                warnings = append(warnings, "Tor did not start ("+err.Error()+") - you can retry from the Tor tab once your network allows it.")
                                torOK = false
                        } else {
                                return warnings, fmt.Errorf("tor: %v", err)
                        }
                }
        }
        if torOK {
                serverKinds := []Kind{}
                for _, k := range kinds {
                        if k != KindRelay {
                                serverKinds = append(serverKinds, k)
                        }
                }
                if err := e.waitForOnions(ctx, serverKinds, 4*time.Minute); err != nil {
                        if opts.AllowTorFailure {
                                warnings = append(warnings, "Onion addresses were not published in time - Tor may still be bootstrapping. Press Retry on the Tor tab when it connects.")
                        } else {
                                return warnings, fmt.Errorf("onion services did not publish in time - check the Tor log")
                        }
                } else {
                        if warns := e.setOnionHosts(serverKinds); len(warns) > 0 {
                                warnings = append(warnings, warns...)
                        }
                        _ = e.State.With(func(st *State) { st.Tor.OnionReady = true })
                        // A hostname file only means Tor generated the key - the
                        // hidden-service DESCRIPTORS take another few minutes to
                        // spread through the Tor network, and until they do the
                        // apps cannot connect ("server not available"). Verify
                        // actual reachability through Tor before calling it done.
                        e.Log.Step("Verifying the servers are reachable through the Tor network")
                        live := e.waitForOnionReachability(ctx, serverKinds, 3*time.Minute)
                        for _, k := range serverKinds {
                                rec := e.State.Server(k)
                                if !rec.Installed {
                                        continue
                                }
                                if kindIn(k, live) {
                                        e.Log.Detail(Def(k).Name + " is reachable through the Tor network")
                                } else {
                                        warnings = append(warnings, Def(k).Name+": its .onion address is not reachable through the Tor network yet - this is normal during the first minutes after Tor connects (the address may take a few minutes to spread on the Tor network). The Dashboard shows a green ✓ next to the address once a connection succeeds; 'sxops check' verifies it from the terminal.")
                                }
                        }
                }
        }

        // 4. start servers
        for _, k := range kinds {
                if k == KindRelay {
                        continue
                }
                if !e.State.Server(k).Enabled {
                        continue
                }
                e.Log.Step("Starting " + Def(k).Name)
                if err := e.Backend.StartServer(k); err != nil {
                        return warnings, fmt.Errorf("%s: %v", Def(k).Name, err)
                }
        }

        // 5. relay
        if opts.EnableRelay {
                e.Log.Step("Starting chat relay")
                smpAddr := Address(KindSMP, e.State.Settings(), e.State.Server(KindSMP))
                if err := e.startRelay(smpAddr); err != nil {
                        return warnings, fmt.Errorf("relay: %v", err)
                }
                if !torOK {
                        warnings = append(warnings, "The chat relay started, but its address may not be reachable until Tor is connected.")
                }
        }
        _ = e.State.With(func(st *State) { st.Provision = true })
        e.Log.Step("Setup complete - addresses are on the Dashboard")
        return warnings, nil
}

// waitForOnions blocks until every installed server kind has published its
// onion hostname (or ctx / budget expires). Guided setup passes 4 minutes
// (a first-ever publication on a slow network can outstay a shorter
// window); the Tor panel's Connect passes a short budget - the hostname
// files normally exist within seconds of Tor starting, and blocking
// longer keeps an already-bootstrapped Tor looking "stuck in connecting".
func (e *Engine) waitForOnions(ctx context.Context, kinds []Kind, budget time.Duration) error {
        return waitUntil(ctx, budget, 3*time.Second, func() (bool, error) {
                for _, k := range kinds {
                        if e.State.Server(k).Installed {
                                if _, err := e.Backend.ReadOnionHost(k); err != nil {
                                        return false, nil
                                }
                        }
                }
                return true, nil
        })
}

// waitForOnionReachability probes each installed kind's onion service
// through the managed Tor SOCKS port until it answers - the same network
// path a SimpleX app on another device takes. Hidden-service descriptors
// can take several minutes to spread after bootstrap: during that window
// the hostname file exists but clients cannot connect, which is the
// classic "the server does not work in the app" report. Returns the
// kinds that answered within the budget.
func (e *Engine) waitForOnionReachability(ctx context.Context, kinds []Kind, budget time.Duration) []Kind {
        csocks := ClientSocksPortOf(e.State)
        if csocks == 0 {
                return nil
        }
        type target struct {
                k    Kind
                host string
                port int
        }
        var targets []target
        for _, k := range kinds {
                rec := e.State.Server(k)
                if !rec.Installed || rec.OnionHost == "" {
                        continue
                }
                virtual := rec.Port
                if virtual == 0 {
                        virtual = DefaultPort(k)
                }
                targets = append(targets, target{k: k, host: rec.OnionHost, port: virtual})
        }
        live := []Kind{}
        deadline := time.Now().Add(budget)
        for time.Now().Before(deadline) && len(targets) > 0 {
                var remaining []target
                for _, t := range targets {
                        // Cap every probe by the budget still left: a round
                        // that starts just before the deadline used to run
                        // minutes past it (each probe got the full 40s
                        // unconditionally).
                        left := time.Until(deadline)
                        if left <= 0 {
                                remaining = append(remaining, t)
                                continue
                        }
                        per := 40 * time.Second
                        if left < per {
                                per = left
                        }
                        if err := probeTorSOCKSTimeout(csocks, t.host, t.port, per); err != nil {
                                remaining = append(remaining, t)
                        } else {
                                live = append(live, t.k)
                        }
                        select {
                        case <-ctx.Done():
                                return live
                        default:
                        }
                }
                targets = remaining
                if len(targets) == 0 {
                        break
                }
                select {
                case <-ctx.Done():
                        return live
                case <-time.After(5 * time.Second):
                }
        }
        return live
}

// probeOnionsRound probes the onion services of all given kinds through
// the client SOCKS port CONCURRENTLY - one round, no retries, each probe
// bounded by perProbe - and returns the kinds whose onion answered. It is
// the fast, non-blocking reachability check for the Tor panel's Connect:
// after a Tor restart the hidden-service descriptors take minutes to
// spread, and waiting that out inside Connect is what made a connected
// Tor sit on "connecting" for minutes (v1.5.4).
func (e *Engine) probeOnionsRound(kinds []Kind, perProbe time.Duration) []Kind {
        csocks := ClientSocksPortOf(e.State)
        if csocks == 0 || len(kinds) == 0 {
                return nil
        }
        type result struct {
                k  Kind
                up bool
        }
        ch := make(chan result, len(kinds))
        for _, k := range kinds {
                go func(k Kind) {
                        rec := e.State.Server(k)
                        host := rec.OnionHost
                        virtual := rec.Port
                        if virtual == 0 {
                                virtual = DefaultPort(k)
                        }
                        if host == "" {
                                ch <- result{k, false}
                                return
                        }
                        ch <- result{k, probeTorSOCKSTimeout(csocks, host, virtual, perProbe) == nil}
                }(k)
        }
        var live []Kind
        for range kinds {
                if r := <-ch; r.up {
                        live = append(live, r.k)
                }
        }
        return live
}

// kindIn reports whether k is in the list.
func kindIn(k Kind, list []Kind) bool {
        for _, kk := range list {
                if kk == k {
                        return true
                }
        }
        return false
}

// setOnionHosts persists published onion hostnames into state + INI host fields.
func (e *Engine) setOnionHosts(kinds []Kind) []string {
        var warns []string
        for _, k := range kinds {
                if h, err := e.Backend.ReadOnionHost(k); err == nil {
                        if err := e.Provisioner.SetOnionHost(k, h); err != nil {
                                warns = append(warns, fmt.Sprintf("%s: saving onion host: %v", Def(k).Name, err))
                        }
                } else {
                        warns = append(warns, fmt.Sprintf("%s: no onion hostname published yet", Def(k).Name))
                }
        }
        // v1.6.29: the SMP server's HTTPS web page certificate covers the
        // onion host - generate/renew it now that the address is known and
        // enable the page in the INI (servers start after this in a fresh
        // setup; a running server is restarted when the config changed).
        e.SyncSMPWebPage()
        return warns
}

// RetryTor (re)starts Tor, waits for onion publication and records the
// onion hostnames. Used by the Tor panel after a soft-failed setup.
func (e *Engine) RetryTor(ctx context.Context) ([]string, error) {
        if needs, why := e.Backend.NeedsTorInstall(); needs {
                return nil, fmt.Errorf("Tor is not installed: %s", why)
        }
        e.Log.Step("Restarting Tor")
        // TorManager.Start regenerates torrc (reflecting the currently
        // enabled onion services and any bridges) and restarts a running
        // instance so it takes effect - a running Tor never re-reads its
        // config file.
        if err := e.Backend.StartTor(ctx); err != nil {
                // v1.6.37: %w so the Tor panel can recognize its own abort
                // (context.Canceled from the Stop button) through the wrap.
                return nil, fmt.Errorf("tor: %w", err)
        }
        var kinds []Kind
        for _, k := range []Kind{KindSMP, KindXFTP, KindNTF} {
                if e.State.Server(k).Installed {
                        kinds = append(kinds, k)
                }
        }
        if err := e.waitForOnions(ctx, kinds, 90*time.Second); err != nil {
                return nil, fmt.Errorf("the onion hostnames did not appear in time (Tor is connected) - check the Tor log")
        }
        warns := e.setOnionHosts(kinds)
        if len(warns) == 0 {
                _ = e.State.With(func(st *State) { st.Tor.OnionReady = true })
        }
        // One quickly bounded, best-effort probe round instead of the
        // blocking 2-minute retry loop of v1.5.4: hidden-service
        // descriptors take a few minutes to spread after a Tor restart,
        // and waiting that out inside Connect - while the panel still
        // said "Connecting to the Tor network…" - made a connected Tor
        // look stuck in connecting. The Dashboard's periodic probe is
        // the ongoing verifier and shows the green check.
        live := e.probeOnionsRound(kinds, 20*time.Second)
        var spread []string
        for _, k := range kinds {
                if e.State.Server(k).Installed && !kindIn(k, live) {
                        spread = append(spread, Def(k).Name)
                }
        }
        if len(spread) > 0 {
                warns = append(warns, strings.Join(spread, ", ")+": addresses may take a few minutes to spread on the Tor network - the Dashboard shows a green ✓ next to the address once a connection succeeds.")
        }
        return warns, nil
}

// SetTorBridges stores bridge lines and makes them take effect (empty
// list = direct connection again). Called by the Tor panel's bridges
// section.
//
// v1.6.34 fixes two behaviors:
//
//   - A STOPPED Tor is no longer started by applying bridges. v1.6.33
//     always relaunched Tor and waited out its bootstrap, so a simple
//     configuration edit turned into a minutes-long connect attempt the
//     user never asked for. Now the lines are persisted and the torrc
//     is regenerated so the next Connect uses them; Tor stays off.
//
//   - The cached directory documents (consensus, descriptors, guard
//     state) are dropped before the restart. Tor reuses that cache
//     across restarts, and after a bridge change the bootstrap would
//     otherwise run on documents fetched over the PREVIOUS network path
//     (direct, or the old bridges): progress jumps ahead on the stale
//     consensus and then stalls at 25% - "asking for networkstatus
//     consensus" on current Tor - when the fetch through the new
//     bridges does not complete, which reads exactly like "the bridges
//     are broken". A clean cache forces every document through the new
//     bridges. The hidden-service keys live in a separate directory
//     (tor/hs), so the onion addresses stay the same.
//
// The boolean tells the caller whether Tor was restarted (because it
// was running), so the Tor panel can word the result accordingly.
//
// v1.6.35: when the restart's bootstrap never gets past 25% while the
// Tor log accumulates bridge connection failures, waitBootstrap aborts
// early and returns a diagnosis ("the configured bridges are not
// connecting...") instead of silently stalling for the full five
// minutes. The sxops bridge pipeline itself was verified live end to
// end (real tor 0.4.9.14 + real working bridges: clean start, and the
// direct->bridge switch with the cache purge, both reach 100% in under
// 20 seconds) - a persistent 25% stall means the bridge lines are dead,
// offline or blocked, and now it says so.
//
// v1.6.37: the caller's context can abort the restart (the Tor panel's
// Stop button while it says "Connecting…"); a cancelled bootstrap
// returns context.Canceled without marking a connect failure, and the
// UI stops the still-running Tor process.
func (e *Engine) SetTorBridges(ctx context.Context, rawLines []string) (restarted bool, err error) {
        bridges := NormalizeBridgeLines(rawLines)
        if len(bridges) > 0 {
                if err := ValidateBridges(bridges); err != nil {
                        return false, err
                }
        }
        s := e.State.Settings()
        s.TorBridges = bridges
        if err := e.State.SetSettings(s); err != nil {
                return false, err
        }
        running := e.Backend.TorRunning()
        if running {
                // A running Tor only re-reads its config on restart: stop it
                // so the next Start regenerates torrc with the bridges.
                if err := e.Backend.StopTor(); err != nil {
                        return false, fmt.Errorf("stopping Tor: %v", err)
                }
        }
        e.Tor.purgeDirectoryCache()
        if running {
                _, err := e.RetryTor(ctx)
                return true, err
        }
        // Tor is off: regenerate the torrc so the stored bridges are what
        // the next Connect uses - do NOT start Tor here.
        if err := e.Tor.GenerateTorrc(); err != nil {
                return false, err
        }
        return false, nil
}

// HandleSingleHopChange is called right after the SingleHopOnion setting
// was persisted with a NEW value: Tor forbids re-using hidden-service
// keys across anonymous/non-anonymous mode ("Once a hidden service
// directory has been used ... in HiddenServiceSingleHopMode, it can NEVER
// be used again for a hidden service"), so the hidden services are
// regenerated - which necessarily changes the onion addresses. The server
// INIs are re-patched to advertise only the still-valid hosts until the
// new onions publish (next Tor start / RetryTor). A stopped Tor stays
// stopped; a running one is restarted by the user from the Tor panel.
func (e *Engine) HandleSingleHopChange() (string, error) {
        if e.Backend.TorRunning() {
                if err := e.Backend.StopTor(); err != nil {
                        return "", fmt.Errorf("stopping Tor for the single-hop change: %v", err)
                }
        }
        // The chat relay must go too: its https link embeds the OLD SMP
        // onion (dead after the key reset) and, in single-hop mode, it
        // would hold the anonymous-mode SOCKS port that no longer exists.
        // A stale registration makes the app's relay test time out at
        // "Wait response" forever - a fresh address is the only cure.
        if e.Sv.Status(KindRelay).State == ProcRunning || e.Sv.Status(KindRelay).State == ProcStarting {
                if err := e.Sv.Stop(KindRelay, 15*time.Second); err != nil {
                        return "", fmt.Errorf("stopping the chat relay for the single-hop change: %v", err)
                }
        }
        if dir := e.Paths.RelayDir(); dir != "" {
                if entries, err := os.ReadDir(dir); err == nil {
                        for _, en := range entries {
                                if en.Name() != "web" {
                                        _ = os.RemoveAll(filepath.Join(dir, en.Name()))
                                }
                        }
                }
        }
        _ = e.State.UpdateServer(KindRelay, func(r *ServerRec) { r.Fingerprint = "" })
        _ = e.State.With(func(st *State) { st.Tor.RelaySocks = 0 })
        _ = os.Remove(e.Paths.ProcLogFile(KindRelay))
        e.Tor.ResetHiddenServices()
        for _, k := range []Kind{KindSMP, KindXFTP, KindNTF} {
                if e.State.Server(k).Installed {
                        if err := e.patchServerIni(k); err != nil {
                                e.Log.Detail(fmt.Sprintf("%s: re-patching config after the single-hop change: %v", Def(k).Name, err))
                        }
                }
        }
        if err := e.Tor.GenerateTorrc(); err != nil {
                return "", err
        }
        // Tor is started right away (not left for the user to click
        // Connect in the Tor panel): the relay below needs it, and the
        // panel would otherwise show the servers as down until then.
        if err := e.ensureTorRunning(context.Background()); err != nil {
                return "", fmt.Errorf("restarting Tor after the single-hop change: %v", err)
        }
        var kinds []Kind
        for _, k := range []Kind{KindSMP, KindXFTP, KindNTF} {
                if e.State.Server(k).Installed && e.State.Server(k).Enabled {
                        kinds = append(kinds, k)
                }
        }
        if len(kinds) > 0 {
                _ = e.waitForOnions(context.Background(), kinds, 90*time.Second)
                _ = e.setOnionHosts(kinds) // also re-writes the INI transport hosts
        }
        // Restart the running servers so they re-read their INIs: the
        // advertised host changed with the fresh onion and a stale
        // handshake host can confuse client apps.
        for _, k := range kinds {
                if e.Backend.ServerRunning(k) {
                        if err := e.Backend.StopServer(k); err == nil {
                                _ = e.Backend.StartServer(k)
                        }
                }
        }
        // Re-create the relay address in the background: the registration
        // needs the FRESH onion reachable through Tor, which lags the
        // hostname files by minutes (descriptor spread). The goroutine
        // probes until the SMP onion answers, then starts the relay -
        // the address shows up on the Dashboard once created.
        if e.State.Server(KindRelay).Installed {
                go e.startRelayWhenOnionLive(15 * time.Minute)
        }
        warn := "Single-hop mode changed - the onion addresses were regenerated (Tor requires fresh keys when switching modes).\n" +
                "The OLD onion addresses stop working; the NEW ones appear on the Dashboard\n" +
                "once Tor finishes connecting (in progress now - the green check follows\n" +
                "within minutes as the addresses spread).\n\n" +
                "The chat relay was reset too: its old address embedded the old onion.\n" +
                "A fresh one is being created and will show on the Dashboard.\n" +
                "Re-add the new relay address in the app (Settings - Network & servers -\n" +
                "Chat relay - replace the old address)."
        return warn, nil
}

// EnableNTFServer turns on the optional notification server end to end:
// it sets up and starts the private PostgreSQL instance (the NTF token
// store is the only SimpleX store that is a database rather than a file),
// installs the ntf-server binary, initializes its config, publishes its
// onion service and starts the server. Idempotent - every step only
// fills what is missing, so it is safe to press again.
func (e *Engine) EnableNTFServer(ctx context.Context) error {
        s := e.State.Settings()
        s.EnableNTF = true
        if err := e.State.SetSettings(s); err != nil {
                return err
        }
        e.Log.Step("Setting up the notification server")
        // 1. the private PostgreSQL instance (no-op + fast when already up)
        if err := e.ensureManagedPostgres(); err != nil {
                return err
        }
        // 2. the ntf-server binary
        if !e.State.Server(KindNTF).Installed {
                rel, _, err := e.Installer.CheckUpdate(ctx, KindNTF)
                if err != nil {
                        return fmt.Errorf("NTF server: %v", err)
                }
                if err := e.Backend.InstallBinary(ctx, KindNTF, rel); err != nil {
                        return fmt.Errorf("NTF server: %v", err)
                }
        }
        _ = e.State.UpdateServer(KindNTF, func(r *ServerRec) { r.Enabled = true })
        // 3. initialize the config (idempotent: skipped when it exists), then
        // re-apply the portable profile either way - the database connection
        // and the subscriber SOCKS proxy follow the current Tor state.
        if e.State.Server(KindNTF).Fingerprint == "" {
                e.Log.Step("Initializing the NTF server configuration")
                args, err := InitArgs(KindNTF, e.State.Settings(), e.Paths)
                if err != nil {
                        return err
                }
                out, err := e.Backend.InitServer(KindNTF, args)
                if err != nil {
                        return fmt.Errorf("NTF server init: %v\n%s", err, truncate(out, 600))
                }
                if err := e.refreshServerRecords(KindNTF); err != nil {
                        return err
                }
        }
        if err := e.patchServerIni(KindNTF); err != nil {
                return fmt.Errorf("NTF server config: %v", err)
        }
        // 4. onion service: the fresh torrc gains the NTF hidden service; a
        // running Tor only re-reads its config on restart. Hidden-service
        // keys of the other servers persist, so their onion addresses are
        // stable across the bounce.
        if err := e.Tor.GenerateTorrc(); err != nil {
                return err
        }
        if err := e.ensureTorRunning(ctx); err != nil {
                return fmt.Errorf("Tor: %v", err)
        }
        if err := e.waitForOnions(ctx, []Kind{KindNTF}, 90*time.Second); err != nil {
                e.Log.Detail("the NTF onion hostname did not appear in time - it is picked up by the next Tor connect (Tor network tab)")
        } else {
                _ = e.setOnionHosts([]Kind{KindNTF})
        }
        // 5. start it (the PreStart hook re-checks the database).
        if err := e.ensurePortsAndStart(KindNTF); err != nil {
                return err
        }
        e.Log.Step("Notification server started - its address is on the Dashboard")
        return nil
}

// DisableNTFServer stops the notification server and the private
// PostgreSQL instance and drops the NTF service from the torrc. The
// data (database, keys, onion hostname) is all kept, so turning it
// back on later is fast and the address does not change. A running
// Tor is left alone on purpose: restarting it would briefly un-verify
// the live onion services, while the stale NTF descriptor hurts
// nothing (nothing listens on it once the server is stopped).
func (e *Engine) DisableNTFServer() error {
        s := e.State.Settings()
        s.EnableNTF = false
        if err := e.State.SetSettings(s); err != nil {
                return err
        }
        _ = e.Backend.StopServer(KindNTF)
        if err := e.stopManagedPostgres(); err != nil {
                e.Log.Detail("stopping the private PostgreSQL database: " + err.Error())
        }
        _ = e.State.UpdateServer(KindNTF, func(r *ServerRec) { r.Enabled = false })
        if err := e.Tor.GenerateTorrc(); err != nil {
                return err
        }
        e.Log.Detail("notification server disabled (data kept) - the torrc takes effect the next time Tor starts")
        return nil
}

// wipeRelayProfile deletes the chat relay's profile database and journal
// (everything in the relay directory except the web dir), so a fresh
// address is created on the next start.
func wipeRelayProfile(paths *Paths) {
        dir := paths.RelayDir()
        entries, err := os.ReadDir(dir)
        if err == nil {
                for _, en := range entries {
                        if en.Name() == "web" {
                                continue
                        }
                        _ = os.RemoveAll(filepath.Join(dir, en.Name()))
                }
        }
}

// ResetRelay deletes the chat relay's profile so its address is created
// fresh on the next start via the current registration path (the SMP
// onion through Tor). The relay's https link embeds its registration
// server permanently, so this is the only way to get rid of a broken or
// stale link (e.g. one the app keeps rejecting as "invalid address")
// and produce a new, correctly-registered one. The relay is stopped
// first; old channel memberships tied to the old address are lost
// (channels keep working through the servers, the relay address is
// re-added in the app).
func (e *Engine) ResetRelay() error {
        if st := e.Sv.Status(KindRelay).State; st == ProcRunning || st == ProcStarting {
                if err := e.Sv.Stop(KindRelay, 15*time.Second); err != nil {
                        return fmt.Errorf("stopping the chat relay: %v", err)
                }
        }
        // wipe the relay profile (DB + journal), keep the web dir if any
        wipeRelayProfile(e.Paths)
        _ = e.State.UpdateServer(KindRelay, func(r *ServerRec) { r.Fingerprint = "" })
        _ = os.Remove(e.Paths.ProcLogFile(KindRelay))
        // start it again - a fresh address is created via the current plan
        return e.StartRelay()
}

// startRelayWhenOnionLive waits until the SMP onion service is
// reachable through the managed Tor client port, then starts the
// chat relay so its one-shot address registration succeeds. Used
// after operations that regenerated the onion keys (single-hop
// toggle) or wiped the relay profile (ResetRelay path): the relay
// NEVER retries a failed registration itself, so the start must be
// delayed until the network path actually works.
func (e *Engine) startRelayWhenOnionLive(wait time.Duration) {
        deadline := time.Now().Add(wait)
        for time.Now().Before(deadline) {
                smpAddr := Address(KindSMP, e.State.Settings(), e.State.Server(KindSMP))
                if !strings.Contains(smpAddr, ".onion") {
                        break // public host: reachable without waiting
                }
                if host, port, ok := onionHostPort(smpAddr); ok {
                        if err := probeTorSOCKS(ClientSocksPortOf(e.State), host, port); err == nil {
                                if err := e.startRelay(smpAddr); err != nil {
                                        e.Log.Detail("chat relay: delayed start failed: " + err.Error())
                                }
                                return
                        }
                }
                if e.Sv.Status(KindRelay).State == ProcRunning {
                        return // started by the user meanwhile
                }
                time.Sleep(30 * time.Second)
        }
        e.Log.Detail("chat relay: not started - the SMP onion service was not reachable through Tor in time; press Start on the Chat relay row once the Dashboard shows the green check")
}

// PatchIni re-applies the portable profile to a server's INI (exported
// for the settings UI: the advertised hosts change with the public host).
func (e *Engine) PatchIni(k Kind) error {
        _, err := e.patchServerIniChanged(k)
        return err
}

// ReloadServerConfig re-applies the portable profile to a server's INI
// and restarts the server when it is running, so INI-only settings
// (XFTP file expiry and storage quota, the info-pages web server, the
// source code link) take effect right away instead of at the next
// start. The chat relay has no INI configuration.
func (e *Engine) ReloadServerConfig(k Kind) error {
        if k == KindRelay {
                return fmt.Errorf("the chat relay has no INI configuration")
        }
        if !e.State.Server(k).Installed {
                return nil // nothing to reload yet - applied at provisioning
        }
        if _, err := e.patchServerIniChanged(k); err != nil {
                return err
        }
        if err := e.refreshServerRecords(k); err != nil {
                return err
        }
        if !e.Backend.ServerRunning(k) {
                return nil
        }
        // The servers read their INI once at start - a running server
        // must be restarted for the changes to apply.
        if err := e.Backend.StopServer(k); err != nil {
                return err
        }
        return e.Backend.StartServer(k)
}

// patchServerIni applies the portable profile to a server's INI via the
// active backend.
func (e *Engine) patchServerIni(k Kind) error {
        _, err := e.patchServerIniChanged(k)
        return err
}

// patchServerIniChanged is patchServerIni reporting whether the INI
// text actually changed - callers use it to decide whether a running
// server must be restarted to pick the new configuration up.
func (e *Engine) patchServerIniChanged(k Kind) (bool, error) {
        d := Def(k)
        data, err := e.Backend.ReadServerFile(k, d.IniName)
        if err != nil {
                return false, err
        }
        tmp := filepath.Join(os.TempDir(), fmt.Sprintf("sxops-%s.ini", k))
        if err := os.WriteFile(tmp, data, 0o600); err != nil {
                return false, err
        }
        ini, err := LoadIni(tmp)
        if err != nil {
                return false, err
        }
        applyPortableProfile(ini, k, e.State.Settings(), e.State.Server(k),
                ClientSocksPortOf(e.State), ntfDBConnStr(e.State, e.Paths))
        if k == KindSMP {
                // v1.6.29: the embedded HTTPS web page (invite/link landing
                // pages for browsers) - certificate first, so the keys are
                // only enabled when there is a certificate to point at.
                if _, err := ensureWebCert(e.Paths.CfgDir(KindSMP),
                        webCertHosts(e.State.Settings(), e.State.Server(KindSMP))); err != nil {
                        e.Log.Detail("web page certificate: " + err.Error())
                }
                applyWebHttps(ini, e.State.Settings(), e.Paths.CfgDir(KindSMP))
        }
        if ini.Text() == string(data) {
                os.Remove(tmp)
                return false, nil
        }
        if err := os.WriteFile(tmp, []byte(ini.Text()), 0o600); err != nil {
                return false, err
        }
        out, err := os.ReadFile(tmp)
        if err != nil {
                return false, err
        }
        os.Remove(tmp)
        return true, e.Backend.WriteServerFile(k, d.IniName, out)
}

// applyWebHttps enables the SMP server's embedded HTTPS web page in the
// INI ([WEB] https/cert/key - the official invite/link landing pages a
// browser gets when someone opens https://<server>/a#key or the root
// info page). The keys are only written when the certificate exists
// (ensureWebCert generates it once a host - onion or public - is known);
// otherwise they stay commented out and the page serves over plain HTTP
// on the onion's virtual port 80.
func applyWebHttps(ini *IniFile, s Settings, cfgDir string) {
        certPath, keyPath := webCertPaths(cfgDir)
        if s.EnableInfoPages && webCertExists(cfgDir) {
                // keep a port the conflict resolution may have moved
                port := DefaultWebHttpsPort(KindSMP)
                if v, ok := ini.Get("WEB", "https"); ok {
                        if p, err := strconv.Atoi(strings.TrimSpace(v)); err == nil && p > 0 {
                                port = p
                        }
                }
                ini.Set("WEB", "https", itoa(port))
                ini.Set("WEB", "cert", certPath)
                ini.Set("WEB", "key", keyPath)
        } else {
                ini.Disable("WEB", "https")
                ini.Disable("WEB", "cert")
                ini.Disable("WEB", "key")
        }
}

// syncSMPWebPage brings the SMP server's HTTPS web page in sync with
// the current hosts: (re)generates the certificate when its SANs do
// not cover them, re-patches the INI, restarts a running server when
// something actually changed (the smp-server reads its INI and the
// certificate files once at start), and bounces a running Tor when its
// torrc lacks the virtual-443 mapping (a running Tor never re-reads
// its torrc; the onion keys persist, the address stays the same).
func (e *Engine) SyncSMPWebPage() {
        rec := e.State.Server(KindSMP)
        if !rec.Installed {
                return
        }
        regen, err := ensureWebCert(e.Paths.CfgDir(KindSMP), webCertHosts(e.State.Settings(), rec))
        if err != nil {
                e.Log.Detail("web page certificate: " + err.Error())
                return
        }
        changed, err := e.patchServerIniChanged(KindSMP)
        if err != nil {
                e.Log.Detail("SMP web page: " + err.Error())
                return
        }
        _ = e.refreshServerRecords(KindSMP)
        if (regen || changed) && e.Backend.ServerRunning(KindSMP) {
                e.Log.Detail("restarting the SMP server so its HTTPS web page follows the new hosts/certificate")
                if err := e.Backend.StopServer(KindSMP); err == nil {
                        _ = e.Backend.StartServer(KindSMP)
                }
        }
        if e.Tor.Running() && !e.Tor.torrcHasDefaultPorts() {
                e.Log.Detail("restarting Tor so the onion service maps virtual port 443 to the HTTPS link page")
                ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
                defer cancel()
                if err := e.Tor.Start(ctx); err != nil {
                        e.Log.Detail("HTTPS link page: " + err.Error())
                }
        }
}

// EnsureSMPWebPage is the one-time v1.6.29 upgrade: installations set
// up by older sxops have no [WEB] https in the SMP INI, so opening one
// of the https:// short links in a browser was refused (nothing
// listened on the port browsers connect to). This enables the embedded
// HTTPS web page and restarts the running SMP server and Tor so it
// takes effect (syncSMPWebPage). Idempotent: once the INI carries the
// key AND the certificate is adequate, this is a no-op.
//
// v1.6.30: the guard also heals v1.6.29 installs - their 2048-bit web
// certificate is rejected by the smp-server itself at start
// (checkHTTPSCredentials requires 4096-bit RSA), which left the SMP
// server unable to come up at all. certHostsCovered fails such
// certificates, so SyncSMPWebPage regenerates them as 4096-bit.
func (e *Engine) EnsureSMPWebPage() {
        rec := e.State.Server(KindSMP)
        if !rec.Installed || !e.State.Settings().EnableInfoPages {
                return
        }
        certPath, _ := webCertPaths(e.Paths.CfgDir(KindSMP))
        if e.smpIniHasWebHttps() && certHostsCovered(certPath, webCertHosts(e.State.Settings(), rec)) {
                return
        }
        e.Log.Detail("enabling the server's HTTPS link page: a https:// contact address opened in a browser now shows the SimpleX invite page (the certificate is self-signed, browsers ask once to accept it)")
        e.SyncSMPWebPage()
}

// smpIniHasWebHttps reports whether the SMP INI has the [WEB] https key
// active (the embedded HTTPS web page enabled).
func (e *Engine) smpIniHasWebHttps() bool {
        data, err := e.Backend.ReadServerFile(KindSMP, Def(KindSMP).IniName)
        if err != nil {
                return false
        }
        tmp := filepath.Join(os.TempDir(), "sxops-smp-web.ini")
        if err := os.WriteFile(tmp, data, 0o600); err != nil {
                return false
        }
        defer os.Remove(tmp)
        ini, err := LoadIni(tmp)
        if err != nil {
                return false
        }
        v, ok := ini.Get("WEB", "https")
        return ok && strings.TrimSpace(v) != ""
}

// applyPortableProfile holds the single source of truth of INI patches.
// socks is the effective Tor SOCKS client port and ntfDB the database
// connection string for the NTF server (both ignored by other kinds).
func applyPortableProfile(ini *IniFile, k Kind, s Settings, rec *ServerRec, socks int, ntfDB string) {
        ini.Set("TRANSPORT", "port", itoa(DefaultPort(k)))
        switch k {
        case KindXFTP, KindNTF:
                ini.Set("TRANSPORT", "control_port", itoa(DefaultControlPort(k)))
                // control-port tokens are generated once and kept stable
                // across re-patches: rotating them on every save would
                // desync the state records from a running server.
                if _, ok := ini.Get("AUTH", "control_port_admin_password"); !ok {
                        ini.Set("AUTH", "control_port_admin_password", randomToken(18))
                }
                if _, ok := ini.Get("AUTH", "control_port_user_password"); !ok {
                        ini.Set("AUTH", "control_port_user_password", randomToken(18))
                }
                if s.PasswordProtect {
                        ini.Set("AUTH", "create_password", s.QueuePassword)
                }
        }
        // The SMP and XFTP info web pages render the server information
        // section (source code link included - AGPLv3) only when
        // [INFORMATION] source_code is set. SMP init writes it via its
        // --source-code flag; XFTP init only leaves it commented out, so
        // the profile asserts it. The NTF server ignores the section
        // (it has no serverPublicInfo parsing) and is skipped.
        if k == KindSMP || k == KindXFTP {
                ini.Set("INFORMATION", "source_code", simplexmqSourceRepo)
        }
        if k == KindXFTP {
                // File expiry is read at server start; sxops exposes it in the
                // settings panel.
                ini.Set("STORE_LOG", "expire_files_hours", itoa(xftpExpireHours(s)))
                if q := xftpQuotaGB(s); q > 0 {
                        ini.Set("FILES", "storage_quota", itoa(q)+"gb")
                } else {
                        // Quota off: the key must be ABSENT - the file server treats
                        // a missing storage_quota as "no limit" (fromMaybe maxBound),
                        // while an EMPTY value would fail to parse and abort startup,
                        // so the key is commented out rather than blanked.
                        ini.Disable("FILES", "storage_quota")
                }
        }
        if s.EnableInfoPages && DefaultWebPort(k) > 0 {
                ini.Set("WEB", "http", itoa(DefaultWebPort(k)))
        } else {
                ini.Disable("WEB", "http")
        }
        // v1.6.39: [STORE_LOG] log_stats is the upstream switch for the
        // daily statistics a server collects into its LOCAL log file
        // (verified in simplexmq Server/Main.hs: logStats = settingIsOn
        // "STORE_LOG" "log_stats" ini; the file server reads the same
        // key). The numbers never leave the computer - the public info
        // page renders only a "Stats enabled" yes/no line (confirmed on
        // the official servers' pages). sxops used to force it on for the
        // SMP server; the ServerStats setting now decides (default off)
        // and the key is always written explicitly - hand-edited values
        // are normalized on the next patch, like every owned key. The NTF
        // server keeps log_stats on: the flag only feeds its local log.
        switch k {
        case KindSMP, KindXFTP:
                if s.ServerStatsEnabled(k) {
                        ini.Set("STORE_LOG", "log_stats", "on")
                } else {
                        ini.Set("STORE_LOG", "log_stats", "off")
                }
        case KindNTF:
                ini.Set("STORE_LOG", "log_stats", "on")
        }
        if k == KindNTF {
                // The NTF token store is a PostgreSQL database (the only
                // SimpleX store that is not a local file) - sxops runs a
                // private unprivileged instance and points the server at it.
                if ntfDB != "" {
                        ini.Set("STORE_LOG", "db_connection", ntfDB)
                }
                // The NTF server SUBSCRIBES to SMP servers to push
                // notifications - SMP servers on .onion hosts are reachable
                // only through the local Tor SOCKS port, exactly like the
                // chat relay's registration path (socks_mode defaults to
                // "onion": the proxy is used for .onion hosts only).
                if socks > 0 {
                        ini.Set("SUBSCRIBER", "socks_proxy", "127.0.0.1:"+itoa(socks))
                }
        }
        hosts := rec.OnionHost
        if s.PublicHost != "" {
                hosts = s.PublicHost + "," + rec.OnionHost
        }
        if hosts != "" {
                ini.Set("TRANSPORT", "host", hosts)
        }
}

func itoa(i int) string { return fmt.Sprintf("%d", i) }

// refreshServerRecords reads fingerprint + control tokens from the INI.
func (e *Engine) refreshServerRecords(k Kind) error {
        d := Def(k)
        data, err := e.Backend.ReadServerFile(k, d.IniName)
        if err != nil {
                return err
        }
        tmp := filepath.Join(os.TempDir(), fmt.Sprintf("sxops-%s-r.ini", k))
        os.WriteFile(tmp, data, 0o600)
        defer os.Remove(tmp)
        ini, err := LoadIni(tmp)
        if err != nil {
                return err
        }
        fpData, err := e.Backend.ReadServerFile(k, "fingerprint")
        if err != nil {
                return fmt.Errorf("fingerprint: %v", err)
        }
        admin, _ := ini.Get("AUTH", "control_port_admin_password")
        user, _ := ini.Get("AUTH", "control_port_user_password")
        return e.State.UpdateServer(k, func(r *ServerRec) {
                r.Fingerprint = strings.TrimSpace(string(fpData))
                r.ControlAdmin = admin
                r.ControlUser = user
                r.Port = DefaultPort(k)
                r.ControlPort = DefaultControlPort(k)
                r.WebPort = DefaultWebPort(k)
                // v1.6.29: the HTTPS web page port follows the INI when the
                // key is active (the port-conflict resolution may have moved
                // it); an inactive key keeps the recorded value - the torrc
                // needs the local target even while no certificate exists
                // yet, and a stale value is ignored when info pages are off.
                if v, ok := ini.Get("WEB", "https"); ok {
                        if p, err := strconv.Atoi(strings.TrimSpace(v)); err == nil && p > 0 {
                                r.WebHttpsPort = p
                        }
                }
        })
}

// StartRelay starts the chat relay with the full reliability logic
// (Tor-first startup, registration-path decision, address watcher,
// stuck-relay healing). Exported for the GUI's per-component Start
// button and updates.
func (e *Engine) StartRelay() error {
        s := e.State.Settings()
        smpAddr := Address(KindSMP, s, e.State.Server(KindSMP))
        return e.startRelay(smpAddr)
}

// startRelay launches the chat relay natively. Servers start once Tor
// is running - there is no direct/no-Tor fallback for the onion path.
// The relay's address is created exactly once (first start) and a failed
// registration is never retried by the CLI, so before a fresh registration
// sxops makes sure (a) Tor is running (starting it now when it is not) and
// (b) the SMP onion service is reachable through it.
func (e *Engine) startRelay(smpAddress string) error {
        // v1.6.4: stop a chat relay that is somehow still running without
        // the own SMP server (adopted from an older sxops run) BEFORE
        // deciding anything else - the CLI of such a relay falls back to
        // the official SimpleX servers whenever it picks a server itself.
        e.StopRelayIfOwnSMPMissing()
        s := e.State.Settings()
        torSocks := ClientSocksPortOf(e.State)
        plan := relayPlanSync(s, smpAddress, torSocks)
        // v1.6.0: lock the relay's file uploads to the own XFTP server as
        // well (when one is installed) - the CLI's preset XFTP list is
        // replaced, just like -s does for SMP.
        plan.XFTP = Address(KindXFTP, s, e.State.Server(KindXFTP))

        relayRec := e.State.Server(KindRelay)
        hasAddress := relayRec != nil && strings.HasPrefix(relayRec.Fingerprint, "http")
        // v1.6.0: the relay CLI is LOCKED to the own SMP server (-s) and
        // registers its link on it (--relay-address-server) - never on
        // the official SimpleX servers, and no other server is ever
        // contacted for new connections. Without an own SMP address there
        // is nothing to run on, so the relay refuses to start rather than
        // silently falling back to official infrastructure.
        if plan.Register == "" || strings.Contains(plan.Register, "YOUR-SERVER-HOST") {
                return fmt.Errorf("the chat relay registers its address on your own SMP server only - start the SMP server (and Tor) first, then start the chat relay")
        }
        if plan.Register != "" && strings.Contains(plan.Register, ".onion") {
                if err := e.ensureTorRunning(context.Background()); err != nil {
                        return err
                }
                if !hasAddress {
                        if err := e.waitOnionReachable(smpAddress); err != nil {
                                return fmt.Errorf("%v - the chat relay was not started: its address registration would fail; press Start again once the Tor network tab shows connected", err)
                        }
                }
        } else if plan.Register != "" && !hasAddress {
                // registering on a public host: still Tor-first for consistency
                if err := e.ensureTorRunning(context.Background()); err != nil {
                        return err
                }
        }

        args := relayArgsWith(s, e.Paths, plan)
        if err := e.Sv.StartWithArgs(KindRelay, args, nil); err != nil {
                return err
        }
        // Record the SOCKS port this relay run was started with (see
        // relaySocksOfPlan): the Statuses watchdog restarts the relay when
        // it drifts from the effective client port.
        _ = e.State.With(func(st *State) { st.Tor.RelaySocks = relaySocksOfPlan(plan) })
        // watch for the relay address in the log file (the CLI prints the
        // https link exactly once, on first start; slow Tor can delay it well
        // past a couple of minutes, so watch for the whole run). When the
        // relay's one-shot startup address creation failed with a BROKER
        // network error (the SMP onion service was not reachable through Tor
        // yet - the relay NEVER retries), restart the relay once the onion is
        // reachable, so the address gets created on the fresh start.
        go func() {
                deadline := time.Now().Add(15 * time.Minute)
                started := time.Now()
                restarts := 0
                for time.Now().Before(deadline) {
                        switch e.Sv.Status(KindRelay).State {
                        case ProcRunning, ProcStarting:
                        default:
                                return // stopped - Statuses() recovers via /sa when needed
                        }
                        if data, err := os.ReadFile(e.Paths.ProcLogFile(KindRelay)); err == nil {
                                if addr := ExtractRelayAddress(string(data)); addr != "" {
                                        _ = e.State.UpdateServer(KindRelay, func(r *ServerRec) {
                                                r.Fingerprint = addr
                                        })
                                        return
                                }
                                if restarts < 2 && time.Since(started) > 45*time.Second &&
                                        relayStartupBrokerFailed(string(data)) &&
                                        strings.Contains(smpAddress, ".onion") {
                                        if host, port, ok := onionHostPort(smpAddress); ok {
                                                if err := probeTorSOCKS(ClientSocksPortOf(e.State), host, port); err == nil {
                                                        restarts++
                                                        e.Log.Detail("chat relay: restarting it - its startup connection to the SMP server failed before the onion service was reachable through Tor")
                                                        _ = e.Sv.Stop(KindRelay, 15*time.Second)
                                                        if err := e.Sv.StartWithArgs(KindRelay, args, nil); err != nil {
                                                                e.Log.Detail("chat relay restart failed: " + err.Error())
                                                                return
                                                        }
                                                        started = time.Now()
                                                        continue // keep watching the fresh run's log
                                                }
                                        }
                                }
                        }
                        time.Sleep(2 * time.Second)
                }
        }()
        return nil
}

// relayStartupBrokerFailed reports whether the relay's current-run log
// shows the fatal one-shot startup failure: a BROKER network error
// (connection to the SMP server timed out) and no created address.
func relayStartupBrokerFailed(log string) bool {
        return !strings.Contains(log, "address is created") &&
                strings.Contains(log, "smp agent error: BROKER")
}

// relayAddrRecovery guards the one-shot `/sa` recovery so at most one
// recovery attempt runs at a time (Statuses is polled every few seconds).
var relayAddrRecoveryRunning bool

// relaySocksHealRunning single-flights the SOCKS-drift restart.
var relaySocksHealRunning bool

// watchRelaySocksDrift restarts a RUNNING chat relay whose recorded
// start-up SOCKS port no longer matches the effective client port
// (ClientSocksPortOf). This is the "running but deaf" failure: Tor
// restarted on different ports (or single-hop mode was toggled, or
// the distro Tor grabbed 9050) and the relay still holds the old,
// dead SOCKS port in its --socks-proxy. The process looks healthy,
// the https link is valid, but no subscription ever reaches it - the
// app's relay test then times out at "Wait response" (the relay
// never sees the challenge). A restart re-creates the agent client
// with the current port. No-op when the ports match or the relay is
// not running; only heals when Tor is up so a down Tor does not
// flap the relay.
func (e *Engine) watchRelaySocksDrift() {
        st := e.State.Get()
        rec := st.Servers[KindRelay]
        if rec == nil || !rec.Installed {
                return
        }
        if e.Sv.Status(KindRelay).State != ProcRunning {
                return
        }
        want := ClientSocksPortOf(e.State)
        if want == 0 || !e.Tor.Running() {
                return // Tor down: starting it is the Tor panel's job
        }
        smpAddr := Address(KindSMP, e.State.Settings(), e.State.Server(KindSMP))
        if !relaySocksDrifted(smpAddr, st.Tor.RelaySocks, want) {
                return
        }
        if relaySocksHealRunning {
                return
        }
        relaySocksHealRunning = true
        go func() {
                defer func() { relaySocksHealRunning = false }()
                e.Log.Detail(fmt.Sprintf(
                        "chat relay: restarting it - it still uses the old Tor SOCKS port %d, the current one is %d (the app's relay test would time out)",
                        st.Tor.RelaySocks, want))
                if err := e.Sv.Stop(KindRelay, 15*time.Second); err != nil {
                        e.Log.Detail("chat relay restart (stop) failed: " + err.Error())
                        return
                }
                smpAddr := Address(KindSMP, e.State.Settings(), e.State.Server(KindSMP))
                if err := e.startRelay(smpAddr); err != nil {
                        e.Log.Detail("chat relay restart failed: " + err.Error())
                }
        }()
}

// relaySMPGuardRunning single-flights the own-SMP relay stop guard
// (StopRelayIfOwnSMPMissing) so concurrent status polls never double-stop.
var relaySMPGuardRunning bool

// ownSMPDownReason returns why the own SMP server cannot serve the chat
// relay (or its protocol test) right now, "" when it can. v1.6.1 covered
// the missing address; v1.6.6 adds the server state: a relay whose SMP
// server is disabled, not installed or not running has nothing to relay
// through and sits deaf at best - and its CLI, asked to do anything new,
// falls back to its preset (official) servers. Both the relay and every
// test against it are refused instead.
func (e *Engine) ownSMPDownReason() string {
        s := e.State.Get()
        if ownSMPNotFound(Address(KindSMP, s.Settings, s.Servers[KindSMP])) {
                return "own SMP server not found - start the SMP server (and Tor) first"
        }
        rec := s.Servers[KindSMP]
        if rec == nil || !rec.Installed {
                return "own SMP server is not installed - set it up first"
        }
        if !rec.Enabled {
                return "own SMP server is disabled - enable it first"
        }
        switch e.Sv.Status(KindSMP).State {
        case ProcRunning, ProcStarting:
                return ""
        default:
                return "own SMP server is not running - start it first"
        }
}

// StopRelayIfOwnSMPMissing stops a RUNNING chat relay when the own SMP
// server cannot serve it - its address is absent or still the
// placeholder (v1.6.1), or the server is disabled, not installed or
// simply not running (v1.6.6: a relay on a dead own SMP server sits
// deaf at best, and its CLI consults its preset (official) servers
// whenever it picks a server itself). v1.6.4: the chat relay only ever
// runs locked to the own SMP server - sxops refuses to start it without
// one, and a relay still running in that state (typically adopted from
// a previous sxops run started before the -s lock) is stopped instead
// of being left running. Returns true when a relay was stopped.
//
// Called from every relay start path (startRelay), the status poll
// (Statuses - which the GUI runs every few seconds) and `sxops check`,
// so a relay without its own SMP server cannot keep running anywhere.
// Idempotent and single-flight; the stop is synchronous (bounded by
// the 15s grace period) so one-shot CLI commands do not exit before
// the relay is actually down.
func (e *Engine) StopRelayIfOwnSMPMissing() bool {
        s := e.State.Get()
        rec := s.Servers[KindRelay]
        if rec == nil || !rec.Installed {
                return false
        }
        switch e.Sv.Status(KindRelay).State {
        case ProcRunning, ProcStarting:
        default:
                return false // not running - the start paths refuse it anyway
        }
        reason := e.ownSMPDownReason()
        if reason == "" {
                return false
        }
        if relaySMPGuardRunning {
                return false
        }
        relaySMPGuardRunning = true
        defer func() { relaySMPGuardRunning = false }()
        e.Log.Detail("chat relay: stopping it - " + reason +
                " (the chat relay only runs with your own SMP server - there is no fallback to other servers)")
        if err := e.Sv.Stop(KindRelay, 15*time.Second); err != nil {
                e.Log.Detail("chat relay stop failed: " + err.Error())
                return false
        }
        return true
}

// RelayForeignLink reports whether the recorded chat relay https link
// is hosted on a server outside this deployment, and its host (used by
// the `check` CLI to warn about dependence on official SimpleX servers).
// v1.6.0: sxops no longer re-registers such a link automatically - the
// CLI being locked to the own server means this can only be a legacy
// link; `sxops check` flags it and the user replaces it via the reset
// (Stop + Reset on the Chat relay row) when convenient.
func (e *Engine) RelayForeignLink() (string, bool) {
        s := e.State.Get()
        rec := s.Servers[KindRelay]
        if rec == nil {
                return "", false
        }
        link := relayAddressOf(rec)
        if link == "" {
                return "", false
        }
        return RelayLinkForeign(Address(KindSMP, s.Settings, s.Servers[KindSMP]), link)
}

// relayHealRunning single-flights the stuck-relay healer; relayHealRestarts
// bounds actual restarts per process.
var relayHealRunning bool
var relayHealRestarts int

// healRelay restarts a running chat relay whose startup address creation
// failed with a BROKER network error (the SMP onion service was not yet
// reachable through Tor - the relay never retries), once the onion IS
// reachable now. Single-flight and bounded per process.
func (e *Engine) healRelay() {
        if relayHealRunning || relayHealRestarts >= 2 {
                return
        }
        relayHealRunning = true
        go func() {
                defer func() { relayHealRunning = false }()
                time.Sleep(2 * time.Second) // debounce the status poll
                st := e.Sv.Status(KindRelay)
                if st.State != ProcRunning {
                        return
                }
                // adopted relays were started by a previous sxops run (Started is
                // zero) - always old enough; in-process relays need 2 minutes
                if !st.Adopted && (st.Started.IsZero() || time.Since(st.Started) < 2*time.Minute) {
                        return // give a fresh relay time to succeed on its own
                }
                data, err := os.ReadFile(e.Paths.ProcLogFile(KindRelay))
                if err != nil || !relayStartupBrokerFailed(string(data)) {
                        return
                }
                s := e.State.Get()
                smpAddr := Address(KindSMP, s.Settings, s.Servers[KindSMP])
                host, port, ok := onionHostPort(smpAddr)
                if !ok || !strings.Contains(smpAddr, ".onion") {
                        return
                }
                if err := probeTorSOCKS(ClientSocksPortOf(e.State), host, port); err != nil {
                        return // onion still not reachable - try again on a later poll
                }
                relayHealRestarts++
                e.Log.Detail("chat relay: restarting it - its startup connection to the SMP server failed before the onion service was reachable through Tor")
                if err := e.Sv.Stop(KindRelay, 15*time.Second); err != nil {
                        e.Log.Detail("chat relay restart (stop) failed: " + err.Error())
                        return
                }
                if err := e.startRelay(smpAddr); err != nil {
                        e.Log.Detail("chat relay restart failed: " + err.Error())
                }
        }()
}

// captureRelayAddress fills the missing relay https address: while the
// relay runs, its log is scanned for the "address is created" line; when
// it is stopped, the address is recovered from the relay profile DB
// asynchronously (the caller shows the address on a later poll).
func (e *Engine) captureRelayAddress(rec *ServerRec, running bool) {
        if running {
                data, err := os.ReadFile(e.Paths.ProcLogFile(KindRelay))
                if err == nil {
                        if addr := ExtractRelayAddress(string(data)); addr != "" {
                                _ = e.State.UpdateServer(KindRelay, func(r *ServerRec) {
                                        r.Fingerprint = addr
                                })
                                rec.Fingerprint = addr
                                return
                        }
                        // the relay may be stuck: its one-shot startup address
                        // creation failed before the SMP onion was reachable
                        // (BROKER network error) - restart it once Tor can reach
                        // the onion, so the address gets created
                        if relayStartupBrokerFailed(string(data)) {
                                e.healRelay()
                        }
                }
                return
        }
        // stopped: recover from the profile DB once (SQLite single-writer)
        if relayAddrRecoveryRunning {
                return
        }
        relayAddrRecoveryRunning = true
        go func() {
                defer func() { relayAddrRecoveryRunning = false }()
                // Give a concurrently stopping relay time to release the DB.
                time.Sleep(3 * time.Second)
                if e.Sv.Status(KindRelay).State == ProcRunning {
                        return // started again meanwhile - log scan will get it
                }
                socks := ClientSocksPortOf(e.State)
                smpAddr := Address(KindSMP, e.State.Settings(), e.State.Server(KindSMP))
                addr, err := RelayAddressFromDB(e.Paths, smpAddr, socks)
                if err != nil {
                        e.Log.Detail("relay address recovery: " + err.Error())
                        return
                }
                _ = e.State.UpdateServer(KindRelay, func(r *ServerRec) {
                        r.Fingerprint = addr
                })
        }()
}

// SyncRelayAddress blocks briefly to recover the relay https address from
// the relay profile DB when it is missing (used by the one-shot CLI
// commands `addresses` / `check`; the relay must be stopped).
func (e *Engine) SyncRelayAddress() {
        s := e.State.Get()
        rec := s.Servers[KindRelay]
        if rec == nil || !rec.Installed || strings.HasPrefix(rec.Fingerprint, "http") {
                return
        }
        if e.Sv.Status(KindRelay).State == ProcRunning {
                return // log scan in Statuses() handles the running case
        }
        socks := ClientSocksPortOf(e.State)
        smpAddr := Address(KindSMP, s.Settings, s.Servers[KindSMP])
        if addr, err := RelayAddressFromDB(e.Paths, smpAddr, socks); err == nil {
                _ = e.State.UpdateServer(KindRelay, func(r *ServerRec) {
                        r.Fingerprint = addr
                })
        }
}

// waitOnionReachable retries a SOCKS5 CONNECT to the SMP onion service
// through the managed Tor instance until it answers (descriptor fetched,
// rendezvous circuit established) or the window closes. This wait runs
// only before the relay's FIRST registration (the address is created
// exactly once and a failed registration is never retried), so the
// budget is generous: after a fresh Tor restart the hidden-service
// descriptor can take a couple of minutes to propagate. Probes are
// quick (10s) and close together (2s) - a healthy Tor answers on the
// first try and the wait returns immediately.
func (e *Engine) waitOnionReachable(smpAddress string) error {
        host, port, ok := onionHostPort(smpAddress)
        if !ok {
                return nil // nothing to probe
        }
        csocks := ClientSocksPortOf(e.State)
        if csocks == 0 {
                return nil
        }
        deadline := time.Now().Add(3 * time.Minute)
        e.Log.Detail("checking that the SMP onion service is reachable through Tor")
        var lastErr error
        for time.Now().Before(deadline) {
                lastErr = probeTorSOCKSTimeout(csocks, host, port, 10*time.Second)
                if lastErr == nil {
                        return nil
                }
                if !e.Backend.TorRunning() {
                        return fmt.Errorf("Tor stopped while waiting for the onion service")
                }
                time.Sleep(2 * time.Second)
        }
        return fmt.Errorf("the SMP onion service did not become reachable through Tor within 3 minutes (last error: %v)", lastErr)
}

// onionHostPort extracts "<host>.onion[:port]"/"scheme://fp@host:port"
// into the hidden-service host and the virtual port to probe.
func onionHostPort(smpAddress string) (string, int, bool) {
        host := smpAddress
        if i := strings.Index(host, "@"); i >= 0 {
                host = host[i+1:]
        }
        if i := strings.Index(host, "://"); i >= 0 {
                host = host[i+3:]
        }
        if i := strings.IndexByte(host, '/'); i >= 0 {
                host = host[:i]
        }
        port := DefaultPort(KindSMP)
        if i := strings.LastIndexByte(host, ':'); i >= 0 {
                if p, err := strconv.Atoi(host[i+1:]); err == nil {
                        port = p
                        host = host[:i]
                }
        }
        if !strings.HasSuffix(host, ".onion") {
                return "", 0, false
        }
        return host, port, true
}

// StartAll brings everything up (used by the GUI Start button / daemon).
// Tor goes up FIRST and must be running before anything else starts:
// servers start once Tor is running - when Tor cannot be started the
// servers are NOT started and the error is returned (retry from the
// Tor network tab).
func (e *Engine) StartAll(ctx context.Context) error {
        if err := e.ensureBackend(ctx); err != nil {
                return err
        }
        // v1.6.4/v1.6.6: a chat relay still running while the own SMP
        // server cannot serve it is stopped first - the relay only ever
        // runs locked to the own SMP server (see StopRelayIfOwnSMPMissing).
        e.StopRelayIfOwnSMPMissing()
        s := e.State.Get()
        torNeeded := false
        for _, k := range []Kind{KindSMP, KindXFTP, KindNTF} {
                if rec := s.Servers[k]; rec != nil && rec.Installed && rec.Enabled {
                        torNeeded = true
                        break
                }
        }
        // Resolve local port conflicts BEFORE starting Tor: the freshly
        // generated torrc then maps the onion virtual ports straight to
        // the local ports the servers will bind (no Tor bounce needed).
        for _, k := range []Kind{KindSMP, KindXFTP, KindNTF} {
                if rec := s.Servers[k]; rec != nil && rec.Installed && rec.Enabled {
                        if moved, err := e.EnsureServerPorts(k); err != nil {
                                e.Log.Detail("warning: " + err.Error())
                        } else {
                                for _, m := range moved {
                                        e.Log.Step(m)
                                }
                        }
                }
        }
        // Tor goes up FIRST: the servers (and the chat relay, whose address
        // registration goes through Tor's SOCKS port) start once Tor is
        // running. A Tor failure is fatal here - no server is started.
        if torNeeded {
                if err := e.Backend.StartTor(ctx); err != nil {
                        return fmt.Errorf("Tor: %v\n\nThe servers were not started - they start once Tor is running.\nPress Connect in the Tor network tab (left), then Start all again.", err)
                }
        }
        var errs []string
        for _, k := range []Kind{KindSMP, KindXFTP, KindNTF} {
                if rec := s.Servers[k]; rec != nil && rec.Installed && rec.Enabled {
                        if err := e.Backend.StartServer(k); err != nil {
                                errs = append(errs, fmt.Sprintf("%s: %v", Def(k).Name, err))
                        }
                }
        }
        if rec := s.Servers[KindRelay]; rec != nil && rec.Installed && rec.Enabled {
                // Tor is up at this point (or not needed): startRelay
                // registers the address through Tor's SOCKS port.
                smpAddr := Address(KindSMP, e.State.Settings(), e.State.Server(KindSMP))
                if err := e.startRelay(smpAddr); err != nil {
                        errs = append(errs, fmt.Sprintf("relay: %v", err))
                }
        }
        if len(errs) > 0 {
                return fmt.Errorf("%s", strings.Join(errs, "; "))
        }
        return nil
}

// ensureBackend is a no-op on Linux: the native backend is always
// usable. It is kept for interface symmetry with future platforms.
func (e *Engine) ensureBackend(ctx context.Context) error {
        return nil
}

// StopAll stops the relay and the servers (Tor keeps running to avoid
// onion circuit churn unless stopTor is set). v1.6.32: the servers are
// stopped in PARALLEL - this used to stop them one after another,
// waiting for each to exit before signalling the next, so the whole
// sequence took the SUM of every server's shutdown and a caller that
// did not wait it out lost everything after the first server (the
// close-window "Stop servers" choice fired StopAll in a
// fire-and-forget goroutine and exited, which left the XFTP server -
// everything after SMP in the old order - running). Signalling every
// server at once and waiting for them together also makes the stop as
// fast as the slowest single server. The relay stops first as before
// (it talks to Tor and the SMP server; stopping it first avoids
// reconnect churn).
func (e *Engine) StopAll(stopTor bool) {
        _ = e.Sv.Stop(KindRelay, 10*time.Second)
        var wg sync.WaitGroup
        for _, k := range []Kind{KindSMP, KindXFTP, KindNTF} {
                wg.Add(1)
                go func(k Kind) {
                        defer wg.Done()
                        _ = e.Backend.StopServer(k)
                }(k)
        }
        wg.Wait()
        if stopTor {
                _ = e.Backend.StopTor()
        }
}

// ComponentStatus is the GUI-facing status of one component.
type ComponentStatus struct {
        Kind      Kind
        Name      string
        Backend   string
        Running   bool
        StateText string
        Version   string
        Address   string
        OnionHost string
        PID       int
        Started   time.Time
        // Optional marks a component that is deliberately not part of this
        // deployment (e.g. the NTF server, which needs PostgreSQL): the
        // dashboard renders it as "off" instead of "not installed".
        Optional bool
        // Note is an optional warning line shown under the component's
        // address on the Dashboard (e.g. a relay link hosted outside this
        // deployment).
        Note string
}

// Statuses aggregates component statuses.
func (e *Engine) Statuses() []ComponentStatus {
        // v1.6.4: enforce the own-SMP rule on every poll - a chat relay
        // still running while the own SMP server is not found is stopped
        // right here (StopRelayIfOwnSMPMissing is a no-op otherwise).
        e.StopRelayIfOwnSMPMissing()
        s := e.State.Get()
        var out []ComponentStatus
        for _, k := range []Kind{KindSMP, KindXFTP, KindNTF, KindRelay} {
                rec := s.Servers[k]
                cs := ComponentStatus{Kind: k, Name: Def(k).Name}
                if rec == nil {
                        cs.StateText = "not set up"
                        out = append(out, cs)
                        continue
                }
                cs.Version = rec.BinVersion
                cs.OnionHost = rec.OnionHost
                cs.Address = Address(k, s.Settings, rec)
                if k == KindRelay {
                        st := e.Sv.Status(k)
                        cs.Backend = "native"
                        cs.Running = st.State == ProcRunning
                        cs.PID = st.PID
                        cs.Started = st.Started
                        cs.StateText = stateText(st.State)
                        if rec.Fingerprint != "" && strings.HasPrefix(rec.Fingerprint, "http") {
                                cs.Address = rec.Fingerprint
                                // v1.6.0: flag a link hosted outside this deployment -
                                // a legacy link (the relay is locked to the own
                                // server now, so this cannot happen for new links).
                                // sxops does NOT re-register it: the note tells
                                // the user the reset replaces it when convenient.
                                if host, foreign := RelayLinkForeign(Address(KindSMP, s.Settings, s.Servers[KindSMP]), rec.Fingerprint); foreign {
                                        cs.Note = "This link is hosted on " + host + " - not your server (a legacy link; the relay now uses only your own SMP server). Replace it via Stop + Reset on this row when convenient - the new link will be on your server."
                                }
                        } else if rec.Installed {
                                // The relay prints its https address exactly once (first
                                // start). Capture it whenever it shows up: scan the current
                                // run's log while running; when stopped, recover it from the
                                // relay profile DB via a one-shot `/sa` (asynchronously).
                                e.captureRelayAddress(rec, cs.Running)
                        }
                        e.watchRelaySocksDrift()
                        // v1.6.4/v1.6.6: say WHY the relay is (held)
                        // stopped when the own SMP server cannot serve
                        // it - the automatic stop would otherwise look
                        // like a mystery on the Dashboard.
                        if rec.Installed && !cs.Running && cs.Note == "" {
                                if reason := e.ownSMPDownReason(); reason != "" {
                                        cs.Note = "Stopped: " + reason + ". The chat relay only runs with your own SMP server (never against other servers) - bring the SMP server up, then start the relay again."
                                }
                        }
                } else {
                        cs.Backend = e.Backend.Name()
                        cs.Running = e.Backend.ServerRunning(k)
                        if cs.Running {
                                cs.StateText = "running"
                        } else if rec.Installed {
                                cs.StateText = "stopped"
                        } else if !rec.Enabled || (k == KindNTF && !s.Settings.EnableNTF) {
                                // deliberately off: the notification server is opt-in
                                // (Settings -> Notification server) - show it as
                                // optional instead of a broken-looking "not installed".
                                cs.StateText = "off (optional)"
                                cs.Optional = true
                        } else {
                                cs.StateText = "not installed"
                        }
                }
                out = append(out, cs)
        }
        return out
}

func stateText(s ProcState) string {
        switch s {
        case ProcRunning:
                return "running"
        case ProcStarting:
                return "starting"
        case ProcStopping:
                return "stopping"
        case ProcFailed:
                return "failed"
        default:
                return "stopped"
        }
}

// unavailableBackend reports a broken environment (kept for interface
// completeness).
type unavailableBackend struct{ reason string }

func (u *unavailableBackend) Name() string { return "unavailable" }
func (u *unavailableBackend) InstallBinary(context.Context, Kind, *Release) error {
        return fmt.Errorf("backend unavailable: %s", u.reason)
}
func (u *unavailableBackend) InitServer(Kind, []string) (string, error) {
        return "", fmt.Errorf("backend unavailable: %s", u.reason)
}
func (u *unavailableBackend) StartServer(Kind) error {
        return fmt.Errorf("backend unavailable: %s", u.reason)
}
func (u *unavailableBackend) StopServer(Kind) error   { return nil }
func (u *unavailableBackend) ServerRunning(Kind) bool { return false }
func (u *unavailableBackend) ReadServerFile(Kind, string) ([]byte, error) {
        return nil, fmt.Errorf("backend unavailable: %s", u.reason)
}
func (u *unavailableBackend) WriteServerFile(Kind, string, []byte) error {
        return fmt.Errorf("backend unavailable: %s", u.reason)
}
func (u *unavailableBackend) ServerLogTail(Kind, int) ([]byte, error) {
        return nil, fmt.Errorf("backend unavailable: %s", u.reason)
}
func (u *unavailableBackend) WriteTorrc(string) error { return nil }
func (u *unavailableBackend) StartTor(context.Context) error {
        return fmt.Errorf("backend unavailable: %s", u.reason)
}
func (u *unavailableBackend) StopTor() error                  { return nil }
func (u *unavailableBackend) TorRunning() bool                { return false }
func (u *unavailableBackend) BootstrapStatus() (string, bool) { return "", false }
func (u *unavailableBackend) ReadOnionHost(Kind) (string, error) {
        return "", fmt.Errorf("backend unavailable: %s", u.reason)
}
func (u *unavailableBackend) NeedsTorInstall() (bool, string) { return true, u.reason }

func (u *unavailableBackend) InstallTor(context.Context) error {
        return fmt.Errorf("backend unavailable: %s", u.reason)
}

func (u *unavailableBackend) TorLogTail(int) ([]byte, error) {
        return nil, fmt.Errorf("backend unavailable: %s", u.reason)
}

func (u *unavailableBackend) ReadStoreLog(Kind, int) ([]byte, error) {
        return nil, fmt.Errorf("backend unavailable: %s", u.reason)
}
