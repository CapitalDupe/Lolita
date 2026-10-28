package ui

import (
        "context"
        "errors"
        "fmt"
        "sort"
        "strings"
        "sync/atomic"
        "time"

        "fyne.io/fyne/v2"
        "fyne.io/fyne/v2/container"
        "fyne.io/fyne/v2/dialog"
        "fyne.io/fyne/v2/theme"
        "fyne.io/fyne/v2/widget"

        "sxops/internal/core"
)

// torView is the Tor network panel: status, retry/stop, one-click
// install, the onion addresses that make the server reachable without
// port forwarding, and a bridges section for censored networks.
type torView struct {
        c    *console
        root fyne.CanvasObject

        // busy is set while a retry / apply / install / stop runs: the
        // background poll must not overwrite the live progress text.
        busy atomic.Bool

        dot    *statusDot
        hint   *widget.Label
        status *widget.Label

        retryBtn   *widget.Button
        stopBtn    *widget.Button
        installBtn *widget.Button

        // persistent onion address rows (updated in place, never rebuilt)
        onionRows map[core.Kind]*onionRow
        onionHint *widget.Label
        onionBox  *fyne.Container

        // bridges section
        bridgeEntry  *widget.Entry
        bridgeApply  *widget.Button
        bridgeStatus *widget.Label

        // live SOCKS port line (for the desktop app's proxy settings)
        socksLbl *widget.Label

        logEntry *widget.Entry

        // connectCancel aborts the in-flight connect / bridge-apply
        // restart (v1.6.37's Stop button while "Connecting…" runs).
        // Main-thread only: set before the worker goroutine starts,
        // cleared in the completion callback.
        connectCancel context.CancelFunc
}

// onionRow is one persistent "kind: onionhost" row.
type onionRow struct {
        kind core.Kind
        name *widget.Label
        addr *widget.Entry
        host string
        obj  fyne.CanvasObject
}

func newTorView(c *console) *torView {
        v := &torView{c: c}

        v.dot = newStatusDot()
        v.hint = widget.NewLabel("Tor makes your server reachable from anywhere without\nconfiguring your router - it is what removes the need for port forwarding.")
        v.hint.Wrapping = fyne.TextWrapWord
        v.hint.TextStyle = fyne.TextStyle{Italic: true}
        v.status = widget.NewLabel("checking…")
        v.status.TextStyle = fyne.TextStyle{Monospace: true}

        // The SOCKS port matters to users connecting a desktop SimpleX
        // app on THIS machine: the app's proxy must point at it. It is
        // not always 9050 - sxops moves Tor's ports when taken.
        v.socksLbl = widget.NewLabel("")
        v.socksLbl.TextStyle = fyne.TextStyle{Bold: true}
        v.socksLbl.Wrapping = fyne.TextWrapWord

        v.retryBtn = widget.NewButtonWithIcon("Connect", theme.MediaPlayIcon(), func() { v.retry() })
        v.retryBtn.Importance = widget.HighImportance
        // showConnectBtn keeps the label honest: "Retry connection" only
        // after a failed connect attempt, plain "Connect" otherwise.

        // When Tor is connected the connect button is replaced by a stop
        // button: there is nothing to retry while everything works, and
        // stopping the Tor network is the meaningful action.
        // v1.6.37: while a connect or bridge-apply runs ("Connecting…" /
        // "Restarting Tor…") the same button aborts it - a stalled
        // bootstrap no longer traps the user for the full wait.
        v.stopBtn = widget.NewButtonWithIcon("Stop", theme.MediaStopIcon(), func() {
                if v.busy.Load() {
                        v.abortConnect()
                        return
                }
                v.confirmStop()
        })
        v.stopBtn.Importance = widget.DangerImportance
        v.stopBtn.Hide()

        v.installBtn = widget.NewButtonWithIcon("Install Tor", theme.DownloadIcon(), func() { v.install() })
        v.installBtn.Hide()

        // ---- onion address rows (persistent) ----
        v.onionRows = map[core.Kind]*onionRow{}
        var orows []fyne.CanvasObject
        for _, k := range []core.Kind{core.KindSMP, core.KindXFTP, core.KindNTF} {
                r := newOnionRow(c, k)
                v.onionRows[k] = r
                orows = append(orows, r.obj)
        }
        v.onionHint = widget.NewLabel("Onion addresses appear here after Tor connects the first time.")
        v.onionHint.TextStyle = fyne.TextStyle{Italic: true}
        orows = append(orows, v.onionHint)
        v.onionBox = container.NewVBox(orows...)

        // ---- Tor bridges ----
        v.bridgeEntry = widget.NewMultiLineEntry()
        v.bridgeEntry.PlaceHolder = "obfs4 203.0.113.1:443 cert=ABCDEFGHIJKLMNOPQRSTUVWXYZ iat-mode=0\n203.0.113.2:443 1234567890abcdef"
        // wrapping keeps long bridge lines from stretching the scroll
        // (and the window) wider than the screen
        v.bridgeEntry.Wrapping = fyne.TextWrapBreak
        v.bridgeEntry.SetMinRowsVisible(3)
        if bridges := c.eng.State.Settings().TorBridges; len(bridges) > 0 {
                v.bridgeEntry.SetText(strings.Join(bridges, "\n"))
        }
        v.bridgeStatus = widget.NewLabel("")
        v.bridgeStatus.Wrapping = fyne.TextWrapWord
        v.bridgeStatus.TextStyle = fyne.TextStyle{Italic: true}
        v.bridgeApply = widget.NewButtonWithIcon("Apply and restart Tor", theme.ConfirmIcon(), func() { v.applyBridges() })

        v.logEntry = widget.NewMultiLineEntry()
        v.logEntry.TextStyle = fyne.TextStyle{Monospace: true}
        // wrapped so long Tor log lines cannot stretch the window wide
        v.logEntry.Wrapping = fyne.TextWrapBreak
        // v1.6.36: a real log pane, not a sliver. The entry must NOT sit
        // inside container.NewVScroll - a VScroll's minimum height is its
        // own ~32px chrome minimum regardless of the content (see the
        // same note in logs.go), which collapsed this box to a single
        // line of text. A wrapping multi-line entry scrolls through the
        // 200-line tail on its own; 14 visible rows is the floor.
        v.logEntry.SetMinRowsVisible(14)
        v.logEntry.Disable()
        v.logEntry.SetText("(Tor log appears here when Tor is running)")

        expl := card("What is this?", "", richLines([]string{
                "Your server is published as a Tor onion service: an address that ends in .onion.",
                "Anyone with the SimpleX app can connect to it from any network -",
                "you never opened a port on your router and your home IP stays private.",
        }))

        bridgesCard := card("Tor bridges", "Only needed when Tor cannot connect on your network (censorship).\nLeave empty for a normal direct connection.", container.NewVBox(
                richLines([]string{
                        "Get bridge lines at https://bridges.torproject.org (select \"Custom Bridges\").",
                        "Paste one bridge per line below and press Apply. obfs4 bridges additionally",
                        "need:  sudo apt install obfs4proxy  - plain IP:port bridges work without it.",
                }),
                v.bridgeEntry,
                container.NewHBox(v.bridgeApply),
                v.bridgeStatus,
        ))

        v.root = container.NewVScroll(container.NewVBox(
                card("Tor connection", "", container.NewVBox(
                        container.NewHBox(v.dot.object()),
                        v.status,
                        container.NewHBox(v.retryBtn, v.installBtn, v.stopBtn),
                        v.hint,
                )),
                card("Use this Tor from the SimpleX desktop app", "The desktop app cannot reach .onion servers directly -\nit must use a SOCKS5 proxy. If the app runs on THIS machine, point it\nat sxops' Tor below (Settings → Network & servers → SOCKS proxy):", container.NewVBox(
                        v.socksLbl,
                        richLines([]string{
                                "Apps on OTHER devices use their own Tor (phones: Orbot; computers:\ninstall the Tor Browser) - not this one. See Dashboard → How to connect.",
                        }),
                )),
                bridgesCard,
                card("Onion addresses", "These become part of the server addresses on the Dashboard.", v.onionBox),
                expl,
                card("Tor log", "The newest lines are at the bottom - scroll inside the box for older ones.", v.logEntry),
        ))
        return v
}

// newOnionRow builds one persistent onion host row.
func newOnionRow(c *console, k core.Kind) *onionRow {
        r := &onionRow{kind: k}
        r.name = widget.NewLabel(core.Def(k).Name)
        r.name.TextStyle = fyne.TextStyle{Bold: true}
        r.addr = monoLabel("")
        // buttons on the name line: keeping them beside the long monospace
        // host in a Border layout clipped them off the right window edge
        r.obj = container.NewVBox(
                widget.NewSeparator(),
                container.NewHBox(r.name, spacer(), copyButton(c.win, func() string { return r.host })),
                r.addr,
        )
        r.obj.Hide()
        return r
}

func (r *onionRow) update(st core.ComponentStatus) {
        if st.OnionHost == "" {
                r.obj.Hide()
                return
        }
        if r.host != st.OnionHost {
                r.host = st.OnionHost
                r.addr.SetText(st.OnionHost)
        }
        r.obj.Show()
}

// refresh is invoked when the tab opens.
func (v *torView) refresh() {
        statuses := v.c.eng.Statuses()
        torSt, torOK := v.c.eng.Backend.BootstrapStatus()
        fyne.Do(func() { v.updateStatus(statuses, torSt, torOK) })
        go v.fetchLog()
}

// updateStatus refreshes chips and onion rows (main thread). While a
// retry / apply / stop is in progress the live progress ticker owns the
// status text and dot, so the background poll leaves them alone.
func (v *torView) updateStatus(statuses []core.ComponentStatus, torSt string, torOK bool) {
        // live SOCKS port line: the CLIENT port (in single-hop mode the
        // service instance runs without one - the client instance serves
        // sxops itself and the desktop app)
        if p := v.c.eng.Tor.ClientSocksPort(); p > 0 {
                if v.c.eng.State.Settings().SingleHopOnion {
                        v.socksLbl.SetText(fmt.Sprintf("127.0.0.1:%d  (client instance - single-hop mode)", p))
                } else {
                        v.socksLbl.SetText(fmt.Sprintf("127.0.0.1:%d", p))
                }
        } else {
                v.socksLbl.SetText("(start Tor - the port appears here)")
        }

        if !v.busy.Load() {
                needs, why := v.c.eng.Backend.NeedsTorInstall()
                torState := v.c.eng.State.Get().Tor
                switch {
                case needs:
                        v.dot.set("Tor not installed", theme.ColorNameError)
                        v.status.SetText(why)
                        v.installBtn.Show()
                        v.installBtn.SetText(installLabel(v.c.eng.Backend.Name()))
                        v.retryBtn.Hide()
                        v.stopBtn.Hide()
                case torOK && core.TorBootstrapped(torSt):
                        v.dot.set("Tor connected", theme.ColorNameSuccess)
                        v.status.SetText("Connected to the Tor network - your server is reachable.")
                        v.installBtn.Hide()
                        v.retryBtn.Hide()
                        v.stopBtn.Show()
                case torOK && !torState.ConnectFailed:
                        // honestly connecting (no prior failed attempt) - the
                        // control port answers and progress is being made
                        v.dot.set("Tor connecting…", theme.ColorNameWarning)
                        v.status.SetText(torSt + "\nThis can take a minute, especially the first time.")
                        v.installBtn.Hide()
                        v.stopBtn.Show()
                        v.showConnectBtn()
                case torOK:
                        // The control port answers but the last connect attempt
                        // FAILED (waitBootstrap timed out). This used to show
                        // "Tor connecting…" forever - the panel never moved to
                        // the failed state while the process stayed alive.
                        v.dot.set("Tor failed to connect", theme.ColorNameError)
                        msg := "The last attempt to connect to the Tor network failed.\nPress Retry connection."
                        if torState.ConnectError != "" {
                                msg = torState.ConnectError + "\n\nPress Retry connection."
                        }
                        v.status.SetText(msg)
                        v.installBtn.Hide()
                        v.stopBtn.Show()
                        v.showConnectBtn()
                default:
                        v.installBtn.Hide()
                        v.stopBtn.Hide()
                        v.showConnectBtn()
                        if torState.ConnectFailed {
                                v.dot.set("Tor failed to connect", theme.ColorNameError)
                                msg := "The last attempt to connect to the Tor network failed.\nPress Retry connection."
                                if torState.ConnectError != "" {
                                        msg = torState.ConnectError + "\n\nPress Retry connection."
                                }
                                v.status.SetText(msg)
                        } else {
                                v.dot.set("Tor not running", theme.ColorNameWarning)
                                v.status.SetText("Tor is installed but not running. Press Connect.")
                        }
                }
        }

        // onion rows (updated in place; relay has no onion of its own)
        anyShown := false
        for _, st := range statuses {
                if st.Kind == core.KindRelay {
                        continue
                }
                if r := v.onionRows[st.Kind]; r != nil {
                        r.update(st)
                        if r.obj.Visible() {
                                anyShown = true
                        }
                }
        }
        if anyShown {
                v.onionHint.Hide()
        } else {
                v.onionHint.Show()
        }

        // bridges summary line
        if !v.busy.Load() {
                // v1.6.34: the apply button says what pressing it does - restart
                // a running Tor, or just save the lines when Tor is off
                // (applying bridges must not start it).
                if label := applyBridgesLabel(v.c.eng.Backend.TorRunning()); v.bridgeApply.Text != label {
                        v.bridgeApply.SetText(label)
                }
                if bridges := v.c.eng.State.Settings().TorBridges; len(bridges) > 0 {
                        kinds := map[string]bool{}
                        for _, br := range bridges {
                                if f := strings.Fields(br); len(f) > 0 {
                                        kinds[f[0]] = true
                                }
                        }
                        var names []string
                        for n := range kinds {
                                names = append(names, n)
                        }
                        sort.Strings(names)
                        base := fmt.Sprintf("%d bridge(s) in use (%s)", len(bridges), strings.Join(names, ", "))
                        if torOK {
                                v.bridgeStatus.SetText(base + " - Tor connects only through them.")
                        } else {
                                v.bridgeStatus.SetText(base + " - Tor is off, they are used the next time it starts.")
                        }
                } else {
                        v.bridgeStatus.SetText("No bridges in use - Tor connects directly.")
                }
        }
}

// showConnectBtn shows the connect/retry button with the label matching
// the situation: "Retry connection" only when the last connect attempt
// failed, "Connect" when Tor is simply not connected (stopped or still
// connecting).
func (v *torView) showConnectBtn() {
        if v.c.eng.State.Get().Tor.ConnectFailed {
                v.retryBtn.SetText("Retry connection")
                v.retryBtn.SetIcon(theme.MediaReplayIcon())
        } else {
                v.retryBtn.SetText("Connect")
                v.retryBtn.SetIcon(theme.MediaPlayIcon())
        }
        v.retryBtn.Show()
}

func installLabel(backend string) string {
        return "Install Tor"
}

// retry (re)connects Tor and re-publishes the onion services. While it
// runs, a ticker shows the live bootstrap progress so the user always
// sees that something is happening (first bootstraps can take minutes).
// v1.6.37: the Stop button stays enabled beside "Connecting…" - a
// stalled bootstrap (dead bridges, a blocked network) can be aborted
// instead of trapping the user for the full wait.
func (v *torView) retry() {
        v.busy.Store(true)
        v.retryBtn.Disable()
        v.stopBtn.Show()
        v.stopBtn.Enable()
        v.retryBtn.SetText("Connecting…")
        v.status.SetText("Restarting Tor and waiting for the onion services\n(this can take a few minutes the first time)…")
        started := time.Now()
        done := make(chan struct{})
        ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
        v.connectCancel = cancel
        go func() { // live progress until the retry finishes
                t := time.NewTicker(2 * time.Second)
                defer t.Stop()
                for {
                        select {
                        case <-done:
                                return
                        case <-t.C:
                                torSt, ok := v.c.eng.Backend.BootstrapStatus()
                                msg := retryStageText(torSt, ok, time.Since(started))
                                fyne.Do(func() {
                                        if !v.busy.Load() {
                                                return
                                        }
                                        v.status.SetText(msg)
                                })
                        }
                }
        }()
        go func() {
                defer cancel()
                warns, err := v.c.eng.RetryTor(ctx)
                close(done)
                fyne.Do(func() {
                        v.busy.Store(false)
                        v.connectCancel = nil
                        v.retryBtn.Enable()
                        v.stopBtn.SetText("Stop")
                        if err != nil {
                                if errors.Is(err, context.Canceled) {
                                        // v1.6.37: the user pressed Stop while
                                        // connecting - no error dialog, no
                                        // "failed" state; just stop the process.
                                        v.stopBtn.Disable()
                                        go v.stoppedMidConnect()
                                        return
                                }
                                v.retryBtn.SetText("Retry connection")
                                v.status.SetText(err.Error())
                                showError(v.c.win, err)
                                go v.refresh() // pick up the Tor log tail for the log card
                                return
                        }
                        v.retryBtn.SetText("Connect")
                        msg := "Tor is connected and your onion addresses are published."
                        if len(warns) > 0 {
                                msg += "\n" + strings.Join(warns, "\n")
                        }
                        v.status.SetText(msg)
                        go v.refresh()
                })
        }()
}

// abortConnect (v1.6.37) cancels an in-flight connect or bridge-apply
// restart: waitBootstrap polls every two seconds, so the flow unwinds
// quickly; the completion handler then stops the still-running Tor
// process (a cancelled bootstrap leaves it alive). No confirm dialog -
// pressing the button IS the decision.
func (v *torView) abortConnect() {
        if v.connectCancel == nil {
                return
        }
        v.stopBtn.Disable()
        v.stopBtn.SetText("Stopping…")
        v.connectCancel()
}

// stoppedMidConnect finishes a user-aborted connect: the context was
// cancelled and the flow returned; the Tor process is still running
// though, so stop it off-thread and report the clean stopped state.
func (v *torView) stoppedMidConnect() {
        err := v.c.eng.Backend.StopTor()
        fyne.Do(func() {
                v.retryBtn.SetText("Connect")
                if err != nil {
                        v.status.SetText("Could not stop Tor: " + err.Error())
                } else {
                        v.status.SetText("Connection stopped - Tor is off.\nPress Connect to try again.")
                }
                go v.refresh()
        })
}

// confirmStop asks before stopping the Tor network.
func (v *torView) confirmStop() {
        msg := widget.NewLabel(
                "Stop the Tor network?\n\n" +
                        "Your onion addresses go offline until Tor is started again and\n" +
                        "the chat relay may lose its connection (it reaches the SMP server\n" +
                        "through Tor). Your servers keep running - restart Tor any time\n" +
                        "with Connect.")
        msg.Wrapping = fyne.TextWrapWord
        d := dialog.NewCustomConfirm("Stop Tor", "Stop Tor", "Cancel", msg, func(ok bool) {
                if ok {
                        v.stopTor()
                }
        }, v.c.win)
        d.Show()
}

// stopTor stops the managed Tor instance.
func (v *torView) stopTor() {
        v.busy.Store(true)
        v.stopBtn.Disable()
        v.retryBtn.Disable()
        v.status.SetText("Stopping Tor…")
        go func() {
                err := v.c.eng.Backend.StopTor()
                time.Sleep(1 * time.Second) // let the process exit settle
                fyne.Do(func() {
                        v.busy.Store(false)
                        v.stopBtn.Enable()
                        v.retryBtn.Enable()
                        if err != nil {
                                v.status.SetText("Could not stop Tor: " + err.Error())
                        } else {
                                v.status.SetText("Tor is stopped. Press Connect to start it again.")
                        }
                        go v.refresh()
                })
        }()
}

// friendlyPhase renders a core bootstrap status like
// `PROGRESS=45 SUMMARY="Connecting to relay"` for the status label.
func friendlyPhase(torSt string) string {
        p := strings.Index(torSt, "PROGRESS=")
        if p < 0 {
                return torSt
        }
        prog := torSt[p+len("PROGRESS="):]
        if i := strings.IndexAny(prog, " \n"); i >= 0 {
                prog = prog[:i]
        }
        if s := strings.Index(torSt, "SUMMARY="); s >= 0 {
                sum := strings.Trim(strings.TrimSpace(torSt[s+len("SUMMARY="):]), "\"")
                if sum != "" {
                        return prog + "% - " + sum
                }
        }
        return prog + "%"
}

// retryStageText picks the status line shown while Connect/Retry runs.
// v1.5.4 said "Connecting to the Tor network…" for the WHOLE flow - even
// after the bootstrap reached 100%, while the onion addresses published
// and were verified - which read as "stuck in connecting" for minutes.
// The stage text now says what is actually happening.
func retryStageText(torSt string, torOK bool, elapsed time.Duration) string {
        if torOK && core.TorBootstrapped(torSt) {
                return "Tor is connected - publishing the onion addresses…\n" +
                        "Addresses may take a few minutes to spread on the Tor network."
        }
        if torOK {
                return "Connecting to the Tor network…\n" + friendlyPhase(torSt)
        }
        return fmt.Sprintf("Starting Tor… (%d seconds so far)", int(elapsed.Seconds()))
}

// applyBridgesLabel is the bridges section's button text: with a
// running Tor the apply restarts it (a running Tor never re-reads its
// torrc); with Tor off it only saves the lines for the next Connect -
// v1.6.34: it must not start Tor on its own.
func applyBridgesLabel(running bool) string {
        if running {
                return "Apply and restart Tor"
        }
        return "Apply bridges"
}

// applyBridges stores the edited bridge lines and makes them take effect
// (an empty box = direct connection again). With Tor running that means
// a restart through the bridges with live bootstrap progress; with Tor
// off (v1.6.34) the lines are only saved - Tor is NOT started - and the
// next Connect uses them.
//
// v1.6.37: the Stop button beside "Connecting…" aborts a restart that
// stalls through dead bridges (the bridge lines stay saved); the TCP
// preflight v1.6.35 ran here was removed - it promised more than a
// plain TCP connect can prove, and the waitBootstrap bridge diagnosis
// (with the log tail) is the honest verdict anyway.
func (v *torView) applyBridges() {
        lines := strings.Split(v.bridgeEntry.Text, "\n")
        wasRunning := v.c.eng.Backend.TorRunning()
        v.busy.Store(true)
        v.bridgeApply.Disable()
        started := time.Now()
        done := make(chan struct{})
        ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
        v.connectCancel = cancel
        if wasRunning {
                v.bridgeApply.SetText("Restarting Tor…")
                v.stopBtn.Show()
                v.stopBtn.Enable()
                v.retryBtn.Disable()
                v.bridgeStatus.SetText("Stopping Tor, applying bridges and waiting for it to connect again…")
                v.status.SetText("Restarting Tor through the bridges…\n(the network data is fetched fresh through them)")
                go func() { // live progress until the apply finishes (mirrors retry)
                        t := time.NewTicker(2 * time.Second)
                        defer t.Stop()
                        for {
                                select {
                                case <-done:
                                        return
                                case <-t.C:
                                        torSt, ok := v.c.eng.Backend.BootstrapStatus()
                                        msg := retryStageText(torSt, ok, time.Since(started))
                                        fyne.Do(func() {
                                                if !v.busy.Load() {
                                                        return
                                                }
                                                v.status.SetText(msg)
                                        })
                                }
                        }
                }()
        } else {
                v.bridgeApply.SetText("Saving…")
                v.bridgeStatus.SetText("Saving the bridge lines - Tor is off, they take effect the next time it starts.")
        }
        resetUI := func() {
                v.busy.Store(false)
                v.bridgeApply.Enable()
                v.bridgeApply.SetText(applyBridgesLabel(v.c.eng.Backend.TorRunning()))
                v.retryBtn.Enable()
                v.stopBtn.SetText("Stop")
                v.connectCancel = nil
        }
        go func() {
                defer cancel()
                restarted, err := v.c.eng.SetTorBridges(ctx, lines)
                close(done)
                fyne.Do(func() {
                        resetUI()
                        if err != nil {
                                if restarted && errors.Is(err, context.Canceled) {
                                        // v1.6.37: the user pressed Stop during the
                                        // bridge restart. The lines ARE saved (and the
                                        // torrc has them) - only the reconnect was
                                        // cut short; stop the still-running Tor.
                                        v.stopBtn.Disable()
                                        go func() {
                                                serr := v.c.eng.Backend.StopTor()
                                                fyne.Do(func() {
                                                        if serr != nil {
                                                                v.bridgeStatus.SetText("Bridges were saved, but stopping Tor failed: " + serr.Error())
                                                        } else {
                                                                v.bridgeStatus.SetText("Bridges were saved - the restart was stopped, Tor is off.\nPress Connect to start Tor through them.")
                                                        }
                                                        go v.refresh()
                                                })
                                        }()
                                        return
                                }
                                if restarted {
                                        // the lines ARE saved and the torrc HAS them -
                                        // only the reconnect through them failed
                                        v.bridgeStatus.SetText("Bridges were saved, but Tor did not connect:")
                                } else {
                                        v.bridgeStatus.SetText("Bridges were not applied:")
                                }
                                l := widget.NewLabel(err.Error())
                                l.Wrapping = fyne.TextWrapWord
                                dialog.NewCustom("Could not apply bridges", "Close", l, v.c.win).Show()
                                go v.refresh()
                                return
                        }
                        if !restarted {
                                v.bridgeStatus.SetText("Bridges saved. Press Connect to start Tor through them.")
                        } else {
                                v.bridgeStatus.SetText("Bridges applied - Tor is connected through them.")
                        }
                        go v.refresh()
                })
        }()
}

// install runs the backend's Tor installer (on Linux it shows the
// package-manager instructions).
func (v *torView) install() {
        v.busy.Store(true)
        v.installBtn.Disable()
        v.installBtn.SetText("Installing…")
        go func() {
                ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
                defer cancel()
                err := v.c.eng.Backend.InstallTor(ctx)
                fyne.Do(func() {
                        v.busy.Store(false)
                        v.installBtn.Enable()
                        if err != nil {
                                // On Linux this is expected: show instructions.
                                l := widget.NewLabel(err.Error())
                                l.Wrapping = fyne.TextWrapWord
                                dialog.NewCustom("How to install Tor", "Close", l, v.c.win).Show()
                                v.status.SetText("Install Tor with your package manager, then press Connect.")
                                return
                        }
                        v.status.SetText("Tor installed. Press Connect to start it.")
                })
        }()
}

// fetchLog tails the Tor log off-thread.
func (v *torView) fetchLog() {
        data, err := v.c.eng.Backend.TorLogTail(200)
        fyne.Do(func() {
                if err != nil {
                        return // keep placeholder text
                }
                if s := strings.TrimSpace(string(data)); s != "" {
                        setLogText(v.logEntry, lastLines(s, 200))
                }
        })
}
