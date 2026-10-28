// sxops - SimpleX Server Ops Console
//
// A Linux GUI application that makes setting up and administering a
// SimpleX Chat server stack as easy as possible: SMP server, XFTP server,
// the chat relay and (optionally) the notification server - safely
// reachable via Tor onion services without any port forwarding.
// On Windows, sxops runs inside WSL2 (one-time manual setup, see README;
// the GUI appears through WSLg).
package main

import (
        "context"
        "flag"
        "fmt"
        "os"
        "os/signal"
        "strings"
        "syscall"
        "time"

        _ "sxops/internal/compat" // glibc 2.35 compatibility (see compat.go)
        "sxops/internal/core"
        "sxops/internal/ui"
        "sxops/internal/remoteapi"
)

const versionInfo = "sxops 1.6.47"

func usage() {
        fmt.Print(`sxops - SimpleX Server Ops Console

Usage:
  sxops                    start the graphical console
  sxops setup              headless guided setup (downloads + initializes + starts)
                            --components smp,xftp,ntf,relay (default: smp,xftp,relay)
  sxops start              start all enabled components
  sxops stop               stop servers and the relay (--tor also stops Tor)
  sxops update             update installed components to the latest release
                           --check only reports available updates
  sxops status             print component status and addresses
  sxops addresses          print client-facing addresses
  sxops check              verify servers work: Tor reachability + the
                           full protocol test (own servers only)
  sxops doctor             run environment diagnostics
  sxops remote             serve authenticated HTTPS API for Android companion
  sxops daemon             supervise servers without GUI (manual)
  sxops version            print version

`)
}

type cliLog struct{ prefix string }

func (c cliLog) Step(msg string)   { fmt.Println("== " + msg) }
func (c cliLog) Detail(msg string) { fmt.Println("   " + msg) }

func main() {
        args := os.Args[1:]
        cmd := "gui"
        if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
                cmd = args[0]
                args = args[1:]
        }
        fs := flag.NewFlagSet("sxops "+cmd, flag.ContinueOnError)
        fs.SetOutput(os.Stderr)

        switch cmd {
        case "version":
                fmt.Println(versionInfo)
                fmt.Println("fyne GUI, simplexmq servers, simplex-chat relay")
        case "gui":
                ui.Run(versionInfo)
        case "doctor":
                eng, err := core.NewEngine(nil)
                if err != nil {
                        fatal(err)
                }
                failed := false
                fmt.Println("sxops diagnostics")
                fmt.Println("  data root:", eng.Paths.Root)
                for _, c := range core.Doctor(eng.Paths) {
                        status := "OK  "
                        if !c.OK {
                                status = "FAIL"
                                failed = true
                        }
                        fmt.Printf("  [%s] %-18s %s\n", status, c.Name, c.Detail)
                        if c.FixHint != "" {
                                fmt.Printf("         fix: %s\n", c.FixHint)
                        }
                }
                if failed {
                        os.Exit(1)
                }
        case "setup":
                comp := fs.String("components", "smp,xftp,relay", "comma-separated: smp,xftp,ntf,relay")
                name := fs.String("relay-name", "", "chat relay display name")
                if err := fs.Parse(args); err != nil {
                        fatal(err)
                }
                eng, err := core.NewEngine(cliLog{})
                if err != nil {
                        fatal(err)
                }
                if *name != "" {
                        s := eng.State.Settings()
                        s.RelayName = *name
                        _ = eng.State.SetSettings(s)
                }
                opts := core.SetupOptions{}
                for _, c := range strings.Split(*comp, ",") {
                        switch strings.TrimSpace(c) {
                        case "smp":
                                opts.EnableSMP = true
                        case "xftp":
                                opts.EnableXFTP = true
                        case "ntf":
                                opts.EnableNTF = true
                        case "relay":
                                opts.EnableRelay = true
                        }
                }
                ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
                defer cancel()
                opts.AllowTorFailure = false
                warnings, err := eng.FullSetup(ctx, opts)
                if len(warnings) > 0 {
                        for _, w := range warnings {
                                fmt.Println("warning:", w)
                        }
                }
                if err != nil {
                        fatal(err)
                }
                printAddresses(eng)
        case "start":
                eng, err := core.NewEngine(cliLog{})
                if err != nil {
                        fatal(err)
                }
                ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
                defer cancel()
                if err := eng.StartAll(ctx); err != nil {
                        fatal(err)
                }
                printStatus(eng)
        case "stop":
                withTor := fs.Bool("tor", false, "also stop Tor")
                if err := fs.Parse(args); err != nil {
                        fatal(err)
                }
                eng, err := core.NewEngine(cliLog{})
                if err != nil {
                        fatal(err)
                }
                eng.StopAll(*withTor)
                fmt.Println("stopped")
        case "update":
                checkOnly := fs.Bool("check", false, "only report available updates, do not install")
                if err := fs.Parse(args); err != nil {
                        fatal(err)
                }
                eng, err := core.NewEngine(cliLog{})
                if err != nil {
                        fatal(err)
                }
                ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
                defer cancel()
                infos := eng.CheckUpdates(ctx)
                updates := 0
                for _, info := range infos {
                        if !info.Installed {
                                continue
                        }
                        switch {
                        case info.Err != "":
                                fmt.Printf("  %-12s check failed: %s\n", info.Name+":", info.Err)
                        case info.Available:
                                updates++
                                fmt.Printf("  %-12s update available: %s -> %s\n", info.Name+":", or(info.Current, "?"), info.Latest)
                        default:
                                fmt.Printf("  %-12s up to date (%s)\n", info.Name+":", info.Latest)
                        }
                }
                if updates == 0 {
                        fmt.Println("Everything is up to date.")
                        return
                }
                if *checkOnly {
                        fmt.Println("Run 'sxops update' to install them.")
                        return
                }
                n, problems := eng.UpdateAll(ctx)
                for _, p := range problems {
                        fmt.Println("warning:", p)
                }
                fmt.Printf("updated %d component(s)\n", n)
                if n > 0 {
                        printStatus(eng)
                }
        case "status":
                eng, err := core.NewEngine(nil)
                if err != nil {
                        fatal(err)
                }
                printStatus(eng)
        case "addresses":
                eng, err := core.NewEngine(nil)
                if err != nil {
                        fatal(err)
                }
                // v1.6.3: make the portless links LIVE before printing them
                // (upgrades a pre-1.6.3 torrc; restarts Tor once if needed).
                eng.EnsurePortlessLinks()
                printAddresses(eng)
        case "check":
                eng, err := core.NewEngine(nil)
                if err != nil {
                        fatal(err)
                }
                // v1.6.4/v1.6.6: enforce the own-SMP rule on the relay first -
                // a chat relay still running while the own SMP server cannot
                // serve it (address missing, disabled or not running) is
                // stopped (it only ever runs locked to the own SMP server;
                // there is no fallback to other servers).
                if eng.StopRelayIfOwnSMPMissing() {
                        fmt.Println("== chat relay: stopped - your own SMP server cannot serve it right now (the chat relay only runs with it)")
                }
                // v1.6.3: bring a pre-1.6.3 torrc up to the portless-links
                // layout first (may restart Tor once) so the probes and
                // protocol tests below verify LIVE links.
                eng.EnsurePortlessLinks()
                infos := eng.ProbeOnions()
                if len(infos) == 0 && !eng.State.Get().Provision {
                        fmt.Println("No servers installed yet - run 'sxops setup' first.")
                        return
                }
                bad := 0
                fmt.Println("Reachability through the Tor network:")
                for _, info := range infos {
                        fmt.Println(" ", info.ProbeSummary())
                        if info.Err != "" {
                                bad++
                        }
                }
                // the full protocol test (connect, create/secure/delete
                // queue) - only ever against the deployment's own servers,
                // with the test agent locked to them (-s, v1.6.6). A test
                // that cannot run (nothing installed, no address yet, Tor
                // down, or a relay link sxops must never contact - a
                // foreign host or no usable own SMP server) is skipped;
                // the relay itself is stopped by the guard above.
                fmt.Println("\nFull protocol test:")
                for _, k := range []core.Kind{core.KindSMP, core.KindXFTP, core.KindRelay} {
                        if k == core.KindRelay {
                                eng.SyncRelayAddress()
                                // v1.6.0: a legacy relay link hosted on another
                                // server (e.g. an official one) is NEVER tested -
                                // sxops does not connect to servers it does not
                                // own; the note tells the user how to replace it.
                                if host, foreign := eng.RelayForeignLink(); foreign {
                                        fmt.Printf("\nNote: the chat relay's link is hosted on %s - not your server.\nIt is not tested - sxops never connects to other servers. Replace the link\nvia Stop + Reset on the Chat relay once the SMP server is running.\n", host)
                                }
                        }
                        res := eng.RunServerTest(k)
                        if skippedServerTest(res) {
                                // v1.6.6: say why the relay test was not run -
                                // a silently missing relay line left it unclear
                                // which servers the check had even touched.
                                if k == core.KindRelay && strings.Contains(res.Err, "own SMP server") {
                                        fmt.Println("  Chat relay: not tested -", res.Err)
                                }
                                continue
                        }
                        fmt.Println(" ", res.Summary())
                        if !res.Passed {
                                bad++
                        }
                }
                if bad > 0 {
                        fmt.Println("\nIf the servers here fail: check 'sxops status' and the logs ('sxops' GUI → Logs).")
                        fmt.Println("If they pass but the app says 'Test failed at step Connect':")
                        fmt.Println("the app cannot reach the server - enable Tor IN THE APP (see README).")
                        os.Exit(1)
                }
                fmt.Println("\nAll servers verified working through Tor.")
        case "remote":
                listen := fs.String("listen", "127.0.0.1:8787", "HTTPS API listen address (use a firewall when non-loopback)")
                cert := fs.String("cert", "", "TLS certificate PEM file (required)")
                key := fs.String("key", "", "TLS private key PEM file (required)")
                if err := fs.Parse(args); err != nil { fatal(err) }
                token := os.Getenv("SXOPS_REMOTE_TOKEN")
                if len(token) < 32 { fatal(fmt.Errorf("SXOPS_REMOTE_TOKEN must contain at least 32 characters; generate one with: openssl rand -hex 32")) }
                if *cert == "" || *key == "" { fatal(fmt.Errorf("--cert and --key are required; use a TLS certificate trusted by Android")) }
                eng, err := core.NewEngine(cliLog{})
                if err != nil { fatal(err) }
                fmt.Printf("sxops Android API listening on %s (HTTPS)\n", *listen)
                if err := remoteapi.ServeTLS(*listen, *cert, *key, token, eng); err != nil { fatal(err) }
        case "daemon":
                eng, err := core.NewEngine(cliLog{})
                if err != nil {
                        fatal(err)
                }
                ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
                defer cancel()
                _ = eng.StartAll(ctx)
                fmt.Println("sxops daemon supervising - press Ctrl+C to stop")
                sig := make(chan os.Signal, 1)
                signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
                <-sig
                eng.StopAll(false)
        default:
                usage()
                if cmd != "help" && cmd != "--help" && cmd != "-h" {
                        os.Exit(2)
                }
        }
}

func printStatus(eng *core.Engine) {
        for _, st := range eng.Statuses() {
                state := st.StateText
                if st.Running && st.PID != 0 {
                        state = fmt.Sprintf("%s (pid %d)", state, st.PID)
                }
                fmt.Printf("%-12s %-10s backend=%s version=%s\n", st.Name, state, st.Backend, or(st.Version, "-"))
                if st.OnionHost != "" {
                        fmt.Printf("              onion: %s\n", st.OnionHost)
                }
                // v1.6.4: surface the why-notes (e.g. the relay held stopped
                // because the own SMP server is not found) in the CLI too.
                if st.Note != "" {
                        fmt.Printf("              note: %s\n", st.Note)
                }
        }
        torSt := "not running"
        if b, ok := eng.Backend.BootstrapStatus(); ok {
                torSt = b
        }
        fmt.Printf("Tor:          %s\n", torSt)
        if eng.Tor.Running() {
                for _, info := range eng.ProbeOnions() {
                        fmt.Println(" ", info.ProbeSummary())
                }
        }
}

func printAddresses(eng *core.Engine) {
        s := eng.State.Get()
        fmt.Println("SimpleX server addresses (paste into the app settings):")
        for _, st := range eng.Statuses() {
                if st.Address == "" || strings.Contains(st.Address, "YOUR-SERVER-HOST") {
                        continue
                }
                fmt.Printf("  %-12s %s\n", st.Name+":", st.Address)
        }
        if s.Provision {
                // recover the relay link synchronously when possible (one-shot CLI)
                eng.SyncRelayAddress()
                if rec := eng.State.Get().Servers[core.KindRelay]; rec != nil && strings.HasPrefix(rec.Fingerprint, "http") {
                        fmt.Printf("  %-12s %s\n", "Relay link:", rec.Fingerprint)
                }
        }
}

func or(a, b string) string {
        if a == "" {
                return b
        }
        return a
}

// skippedServerTest reports whether a protocol-test result should be
// omitted from the check output: the test could not RUN at all - nothing
// installed, no address yet, Tor down, the CLI missing, or a relay test
// sxops must never perform (a foreign-hosted link, or an own SMP server
// that is not found / not installed / disabled / not running - v1.6.6).
// Real failures - a test that ran and failed - are always shown.
func skippedServerTest(res core.ServerTestResult) bool {
        if res.Passed || res.Step != "" {
                return false
        }
        switch {
        case strings.Contains(res.Err, "not installed"),
                strings.Contains(res.Err, "not created yet"),
                strings.Contains(res.Err, "no address yet"),
                strings.Contains(res.Err, "Tor is not running"),
                strings.Contains(res.Err, "own SMP server"),
                strings.Contains(res.Err, "not your server"):
                return true
        }
        return false
}

func fatal(err error) {
        fmt.Fprintln(os.Stderr, "error:", err)
        os.Exit(1)
}
