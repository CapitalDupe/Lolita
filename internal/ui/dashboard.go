package ui

import (
        "context"
        "fmt"
        "strings"
        "time"

        "fyne.io/fyne/v2"
        "fyne.io/fyne/v2/container"
        "fyne.io/fyne/v2/layout"
        "fyne.io/fyne/v2/theme"
        "fyne.io/fyne/v2/widget"

        "sxops/internal/core"
)

// dashView is the main status + addresses view.
//
// All component and address rows are created ONCE and updated in place.
// Rebuilding the rows on every status poll used to swap the widget tree
// under the user's cursor/selection every 4 seconds, which caused blank
// flashes in the address entries, "Failed to focus object which is not
// part of the canvas' content" errors when a drag ended on a removed
// entry, and broken text selection.
type dashView struct {
        c    *console
        root fyne.CanvasObject

        headerLbl *widget.Label
        startAll  *widget.Button
        stopAll   *widget.Button

        torDot  *statusDot
        torHint *widget.Label

        compRows map[core.Kind]*compRow
        addrRows map[core.Kind]*addrRow
        addrHint *widget.Label

        // live Tor SOCKS port line in the how-to-connect card
        socksLbl *widget.Label
        howtoBox *fyne.Container

        compBox   *fyne.Container
        addrBox   *fyne.Container
        statsLbl  *widget.Label
        lastStats time.Time
        lastProbe time.Time

        // software updates banner (filled by the background check)
        updateBox *fyne.Container
        updateLbl *widget.Label
        updateBtn *widget.Button
}

func newDashView(c *console) *dashView {
        v := &dashView{c: c}

        v.headerLbl = widget.NewLabel("Your SimpleX server")
        v.headerLbl.TextStyle = fyne.TextStyle{Bold: true}

        v.startAll = widget.NewButtonWithIcon("Start all", theme.MediaPlayIcon(), func() {
                d := busyDialog(c.win, "Starting", "Starting Tor first, then your servers…", func() {
                        ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
                        defer cancel()
                        if err := c.eng.StartAll(ctx); err != nil {
                                showError(c.win, err)
                        }
                })
                d.Show()
                go func() {
                        time.Sleep(3 * time.Second)
                        fyne.Do(func() { v.refresh() })
                }()
        })
        v.startAll.Importance = widget.HighImportance

        v.stopAll = widget.NewButtonWithIcon("Stop all", theme.MediaStopIcon(), func() {
                d := busyDialog(c.win, "Stopping", "Stopping your servers…", func() {
                        c.eng.StopAll(false)
                })
                d.Show()
                go func() {
                        time.Sleep(3 * time.Second)
                        fyne.Do(func() { v.refresh() })
                }()
        })

        v.torDot = newStatusDot()
        v.torHint = widget.NewLabel("")
        v.torHint.Wrapping = fyne.TextWrapWord
        v.torHint.TextStyle = fyne.TextStyle{Italic: true}

        // persistent rows, one per component kind
        v.compRows = map[core.Kind]*compRow{}
        var comps []fyne.CanvasObject
        for _, k := range []core.Kind{core.KindSMP, core.KindXFTP, core.KindNTF, core.KindRelay} {
                r := newCompRow(v, k)
                v.compRows[k] = r
                comps = append(comps, r.obj)
        }
        v.compBox = container.NewVBox(comps...)

        v.addrRows = map[core.Kind]*addrRow{}
        var addrs []fyne.CanvasObject
        for _, k := range []core.Kind{core.KindSMP, core.KindXFTP, core.KindNTF, core.KindRelay} {
                r := newAddrRow(v, k)
                v.addrRows[k] = r
                addrs = append(addrs, r.obj)
        }
        v.addrHint = widget.NewLabel("")
        v.addrHint.TextStyle = fyne.TextStyle{Italic: true}
        v.addrHint.Wrapping = fyne.TextWrapWord
        addrs = append(addrs, v.addrHint)
        v.addrBox = container.NewVBox(addrs...)

        v.statsLbl = widget.NewLabel("")
        v.statsLbl.TextStyle = fyne.TextStyle{Italic: true}
        v.statsLbl.Wrapping = fyne.TextWrapWord

        // Software updates banner: starts hidden, appears when the
        // background check finds a newer release for any component.
        v.updateLbl = widget.NewLabel("")
        v.updateLbl.Wrapping = fyne.TextWrapWord
        v.updateBtn = widget.NewButtonWithIcon("Go to updates", theme.DownloadIcon(), func() {
                c.show("updates")
        })
        v.updateBox = container.NewVBox(
                container.NewHBox(v.updateLbl, v.updateBtn))
        v.updateBox.Hide()
        go v.checkForUpdates()

        header := container.NewBorder(
                nil, nil,
                container.NewVBox(v.headerLbl, container.NewHBox(v.torDot.object(), v.torHint)),
                container.NewVBox(v.startAll, v.stopAll),
        )

        v.root = container.NewVScroll(container.NewVBox(
                header,
                widget.NewSeparator(),
                v.updateBox,
                card("Components", "", v.compBox),
                card("Server addresses", "Paste these into the SimpleX app: Settings → SimpleX servers (or scan the QR code).", v.addrBox),
                card("Live statistics", "", v.statsLbl),
                howToConnectCard(v),
        ))
        return v
}

// compRow is one persistent dashboard component row.
type compRow struct {
        kind   core.Kind
        dot    *statusDot
        name   *widget.Label
        ver    *widget.Label
        onion  *widget.Label
        note   *widget.Label // muted explanation (optional components)
        action *widget.Button
        obj    fyne.CanvasObject
}

func newCompRow(v *dashView, k core.Kind) *compRow {
        r := &compRow{kind: k}
        r.dot = newStatusDot()
        r.name = widget.NewLabel(core.Def(k).Name)
        r.name.TextStyle = fyne.TextStyle{Bold: true}
        r.ver = widget.NewLabel("")
        r.ver.TextStyle = fyne.TextStyle{Italic: true}
        r.onion = widget.NewLabel("")
        r.onion.TextStyle = fyne.TextStyle{Monospace: true}
        r.onion.Wrapping = fyne.TextWrapOff
        r.onion.Hide()
        // NOTE: the full-width monospace onion host here used to inflate
        // the scroll content minimum width (and with it the window) past
        // the screen edge - the display is truncated to the middle.
        r.note = widget.NewLabel("")
        r.note.TextStyle = fyne.TextStyle{Italic: true}
        r.note.Wrapping = fyne.TextWrapWord
        r.note.Hide()
        r.action = widget.NewButtonWithIcon("Start", theme.MediaPlayIcon(), func() {
                v.toggleComponent(k)
        })
        r.action.Importance = widget.HighImportance
        r.action.Hide()
        r.obj = container.NewBorder(
                nil, nil,
                container.NewHBox(r.name, r.dot.object(), r.ver, r.note),
                container.NewHBox(r.onion, r.action),
                widget.NewLabel(""),
        )
        return r
}

// update refreshes the row in place from a status snapshot.
func (r *compRow) update(st core.ComponentStatus) {
        r.dot.set(st.StateText, compStateColor(st))
        r.onion.SetText(truncateMiddle(st.OnionHost, 22))
        if st.OnionHost != "" {
                r.onion.Show()
        } else {
                r.onion.Hide()
        }
        if st.Optional {
                // deliberately not part of this deployment (the NTF server)
                r.ver.SetText("")
                r.ver.Refresh()
                r.onion.Hide()
                r.note.SetText("optional - turn it on in Settings (sxops sets up its database automatically)")
                r.note.Show()
                r.action.Hide()
                return
        }
        r.note.Hide()
        r.ver.SetText(versionText(st))
        if st.StateText == "not set up" {
                r.action.Hide()
                return
        }
        r.action.Show()
        if st.Running {
                r.action.SetText("Stop")
                r.action.SetIcon(theme.MediaStopIcon())
                r.action.Importance = widget.MediumImportance
        } else {
                r.action.SetText("Start")
                r.action.SetIcon(theme.MediaPlayIcon())
                r.action.Importance = widget.HighImportance
        }
        r.action.Refresh()
}

// compStateColor maps a component status to a chip color.
func compStateColor(st core.ComponentStatus) fyne.ThemeColorName {
        switch st.StateText {
        case "running", "connected":
                return theme.ColorNameSuccess
        case "starting", "stopping", "bootstrapping":
                return theme.ColorNameWarning
        case "failed", "error":
                return theme.ColorNameError
        default:
                return theme.ColorNameForeground
        }
}

// addrRow is one persistent dashboard address row (hidden until the
// component has an address).
type addrRow struct {
        kind      core.Kind
        name      *widget.Label
        addr      *widget.Entry
        address   string        // current address (read by the copy/QR closures)
        reach     *widget.Label // live Tor probe verdict (15s cycle)
        relayNote *widget.Label // relay-only: Tor-free usability of the https link
        obj       fyne.CanvasObject
}

func newAddrRow(v *dashView, k core.Kind) *addrRow {
        r := &addrRow{kind: k}
        r.name = widget.NewLabel(core.Def(k).Name)
        r.name.TextStyle = fyne.TextStyle{Bold: true}
        r.addr = monoLabel("")
        // live Tor-network reachability of this address ("does it actually
        // work" - probed through Tor exactly like a SimpleX app would)
        r.reach = widget.NewLabel("")
        r.reach.TextStyle = fyne.TextStyle{Italic: true}
        r.reach.Wrapping = fyne.TextWrapWord
        r.reach.Hide()
        qr := widget.NewButtonWithIcon("QR", theme.ViewFullScreenIcon(), func() {
                showQR(v.c.win, r.name.Text+" address", r.address)
        })
        // The buttons sit on the name line: a Border layout with the long
        // monospace address in the middle used to inflate the row's minimum
        // width past the window edge, clipping the buttons out of view.
        obj := container.NewVBox(
                widget.NewSeparator(),
                container.NewHBox(r.name, spacer(), copyButton(v.c.win, func() string { return r.address }), qr),
                r.addr,
                r.reach,
        )
        if k == core.KindRelay {
                // the relay address is an https link used by SimpleX channels
                hint := widget.NewLabel("Chat relay for SimpleX channels (optional) - in the app: Settings → Network & servers → Chat relay.\nIt is created on the relay's first start; if it is missing, press Start on the Chat relay row above.")
                hint.TextStyle = fyne.TextStyle{Italic: true}
                hint.Wrapping = fyne.TextWrapWord
                obj.Add(hint)
                r.relayNote = widget.NewLabel("")
                r.relayNote.TextStyle = fyne.TextStyle{Italic: true}
                r.relayNote.Wrapping = fyne.TextWrapWord
                obj.Add(r.relayNote)
        }
        r.obj = obj
        r.obj.Hide()
        return r
}

func (r *addrRow) update(st core.ComponentStatus) {
        if st.Address == "" || strings.Contains(st.Address, "YOUR-SERVER-HOST") {
                r.obj.Hide()
                return
        }
        if r.address != st.Address {
                r.address = st.Address
                r.addr.SetText(st.Address)
        }
        r.obj.Show()
        if r.kind == core.KindRelay && r.relayNote != nil {
                if st.Note != "" {
                        r.relayNote.SetText(st.Note)
                } else if strings.Contains(r.address, ".onion") {
                        r.relayNote.SetText("This address contains an .onion server - the app needs Tor/Orbot enabled for it.")
                } else {
                        r.relayNote.SetText("")
                }
        }
}

// setReach shows the Tor-network probe result under the address. The
// relay has no onion of its own (its address is an https link), so only
// the SMP/XFTP/NTF rows get a line.
func (r *addrRow) setReach(info *core.ReachabilityInfo) {
        if info == nil || r.kind == core.KindRelay {
                r.reach.Hide()
                return
        }
        switch {
        case info.Skipped != "":
                r.reach.SetText(info.Skipped)
                r.reach.Importance = widget.MediumImportance
                r.reach.Show()
        case info.Reachable:
                r.reach.SetText("✓ reachable through the Tor network")
                r.reach.Importance = widget.SuccessImportance
                r.reach.Show()
        default:
                r.reach.SetText("✗ not reachable through Tor yet - " + info.Err)
                r.reach.Importance = widget.DangerImportance
                r.reach.Show()
        }
        r.reach.Refresh()
}

// spacer returns an expanding blank that pushes trailing buttons to
// the right edge of an HBox.
func spacer() fyne.CanvasObject { return layout.NewSpacer() }

// truncateMiddle shortens long monospace identifiers for row display:
// "s2cpffpvpy…4br64xek6kad.onion" style - the full value stays available
// in the address rows and the Tor panel.
func truncateMiddle(s string, max int) string {
        if max < 8 || len(s) <= max {
                return s
        }
        half := (max - 1) / 2
        return s[:half] + "…" + s[len(s)-half:]
}

// checkForUpdates runs once shortly after the console opens: when a
// newer release exists for any installed component the dashboard shows
// a banner linking to the update section.
func (v *dashView) checkForUpdates() {
        if !v.c.eng.State.Get().Provision {
                return // nothing installed yet - the wizard handles downloads
        }
        time.Sleep(20 * time.Second) // let the dashboard settle first
        ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
        defer cancel()
        infos := v.c.eng.CheckUpdates(ctx)
        var names []string
        for _, info := range infos {
                if info.Available {
                        names = append(names, info.Name+" ("+info.Current+" → "+info.Latest+")")
                }
        }
        if len(names) == 0 {
                return
        }
        fyne.Do(func() {
                v.updateLbl.SetText("Updates available:  " + strings.Join(names, ",  "))
                v.updateBox.Show()
        })
}

// updateStatuses refreshes chips + rows in place (called on main thread).
func (v *dashView) updateStatuses(statuses []core.ComponentStatus, torSt string, torOK bool) {
        // Tor chip
        if torOK {
                if core.TorBootstrapped(torSt) {
                        v.torDot.set("Tor connected", theme.ColorNameSuccess)
                } else {
                        v.torDot.set("Tor connecting…", theme.ColorNameWarning)
                }
                if !core.TorBootstrapped(torSt) {
                        v.torHint.SetText(torSt)
                } else {
                        v.torHint.SetText("")
                }
        } else {
                v.torDot.set("Tor not running", theme.ColorNameError)
                v.torHint.SetText("")
        }

        // component + address rows (updated in place, never rebuilt)
        anyAddress := false
        for _, st := range statuses {
                if r := v.compRows[st.Kind]; r != nil {
                        r.update(st)
                }
                if r := v.addrRows[st.Kind]; r != nil {
                        r.update(st)
                        if r.obj.Visible() {
                                anyAddress = true
                        }
                }
        }
        if anyAddress {
                v.addrHint.Hide()
        } else {
                if v.c.eng.State.Get().Provision {
                        v.addrHint.SetText("Your addresses appear as soon as Tor is connected.\nOpen the Tor network tab (left) and press Connect -\nthe first connection can take a few minutes.")
                } else {
                        v.addrHint.SetText("Addresses appear here after setup completes.")
                }
                v.addrHint.Show()
        }

        // stats (throttled fetch off-thread)
        if time.Since(v.lastStats) > 10*time.Second {
                v.lastStats = time.Now()
                go v.fetchStats(statuses)
        }

        // live SOCKS port line in the how-to-connect card
        v.howToUpdate()

        // Tor-network reachability self-check (throttled): probes each
        // onion service through Tor exactly like a SimpleX app would.
        if anyAddress && time.Since(v.lastProbe) > 15*time.Second {
                v.lastProbe = time.Now()
                go v.fetchReachability()
        }
}

// fetchReachability probes every onion service through the managed Tor
// instance off-thread; each verdict updates its address row the moment
// it arrives (parallel probes, incremental UI updates - a slow onion
// rendezvous no longer stalls the other verdicts).
func (v *dashView) fetchReachability() {
        v.c.eng.ProbeOnionsAsync(func(info core.ReachabilityInfo) {
                fyne.Do(func() {
                        if r := v.addrRows[info.Kind]; r != nil {
                                r.setReach(&info)
                        }
                })
        })
}

// toggleComponent starts or stops one component, querying the LIVE
// state at click time (the row snapshot may be a few seconds old).
// Starting always goes through the engine's Tor-first path: when Tor is
// down it is started (and bootstrapped) before the component starts.
func (v *dashView) toggleComponent(k core.Kind) {
        if k == core.KindRelay {
                if v.c.eng.Sv.Status(k).State == core.ProcRunning {
                        go func() {
                                _ = v.c.eng.Sv.Stop(k, 10*time.Second)
                                time.Sleep(1 * time.Second)
                                fyne.Do(func() { v.refresh() })
                        }()
                        return
                }
                d := busyDialog(v.c.win, "Starting", "Starting Tor first, then the chat relay…", func() {
                        if err := v.c.eng.StartComponent(k); err != nil {
                                showError(v.c.win, err)
                        }
                })
                d.Show()
                go func() {
                        time.Sleep(3 * time.Second)
                        fyne.Do(func() { v.refresh() })
                }()
                return
        }
        if v.c.eng.Backend.ServerRunning(k) {
                go func() {
                        _ = v.c.eng.Backend.StopServer(k)
                        time.Sleep(1 * time.Second)
                        fyne.Do(func() { v.refresh() })
                }()
                return
        }
        d := busyDialog(v.c.win, "Starting", "Starting Tor first, then "+strings.ToLower(core.Def(k).Name)+"…", func() {
                if err := v.c.eng.StartComponent(k); err != nil {
                        showError(v.c.win, err)
                }
        })
        d.Show()
        go func() {
                time.Sleep(3 * time.Second)
                fyne.Do(func() { v.refresh() })
        }()
}

// fetchStats queries the SMP control port for live numbers.
func (v *dashView) fetchStats(statuses []core.ComponentStatus) {
        for _, st := range statuses {
                if st.Kind != core.KindSMP || !st.Running {
                        continue
                }
                rec := v.c.eng.State.Server(core.KindSMP)
                stats, err := core.FetchStats(core.KindSMP, rec)
                if err != nil {
                        return
                }
                parts := []string{}
                if q := stats.Get("qCount"); q != "0" {
                        parts = append(parts, "queues: "+q)
                }
                if m := stats.Get("msgCount"); m != "0" {
                        parts = append(parts, "stored messages: "+m)
                }
                if c := stats.Get("clients"); c != "0" {
                        parts = append(parts, "connected clients: "+c)
                }
                if len(parts) == 0 {
                        parts = append(parts, "no activity recorded yet")
                }
                text := "SMP: " + strings.Join(parts, "  •  ")
                fyne.Do(func() { v.statsLbl.SetText(text) })
                return
        }
        fyne.Do(func() { v.statsLbl.SetText("Statistics appear when the SMP server is running.") })
}

func versionText(st core.ComponentStatus) string {
        if st.Version == "" {
                return ""
        }
        if i := strings.Index(st.Version, "v"); i >= 0 {
                return st.Version[i:]
        }
        return st.Version
}

// refresh recomputes everything off-thread then updates on main.
func (v *dashView) refresh() {
        statuses := v.c.eng.Statuses()
        torSt, torOK := v.c.eng.Backend.BootstrapStatus()
        fyne.Do(func() { v.updateStatuses(statuses, torSt, torOK) })
}

// howtoCardTitle is the "How to connect" card's title. v1.6.44 dropped
// "(once)" at the operator's request - the card stays useful as a
// reference, not just a first-time checklist.
func howtoCardTitle() string { return "How to connect" }

// howToConnectCard explains connecting step by step and shows the live
// Tor SOCKS port (it is NOT always 9050 - sxops moves Tor's ports when
// they are already taken, and a wrong port in the app's proxy settings
// makes every server test fail with "Test failed at step Connect").
func howToConnectCard(v *dashView) fyne.CanvasObject {
        v.socksLbl = widget.NewLabel("")
        v.socksLbl.TextStyle = fyne.TextStyle{Bold: true}
        v.socksLbl.Wrapping = fyne.TextWrapWord
        objs := []fyne.CanvasObject{}
        for _, l := range howtoLinesTor() {
                if strings.TrimSpace(l) == "{{SOCKS-PORT}}" {
                        objs = append(objs, v.socksLbl)
                        continue
                }
                lb := widget.NewLabel(l)
                lb.Wrapping = fyne.TextWrapWord
                objs = append(objs, lb)
        }
        v.howtoBox = container.NewVBox(objs...)
        return card(howtoCardTitle(), "", v.howtoBox)
}

// howtoLinesTor are the connection steps. The {{SOCKS-PORT}} marker
// line is replaced by the live SOCKS port label.
//
// The SimpleX apps have NO built-in "Use Tor" switch anywhere - they
// need Tor running beside them (phones: just run Orbot; PCs: Tor
// Browser or a system Tor, with the app's SOCKS proxy pointed at it).
// v1.6.44 made the PC steps detailed (Tor Browser install, keep-open,
// exact app setting, per-port alternatives) and the guide now covers
// every server address the Dashboard offers (smp, xftp, relay, ntf),
// not just smp. v1.6.47 adds the Tor Expert Bundle to the PC
// background-Tor alternative - Windows users get tor.exe straight
// from torproject.org instead of installing a whole browser.
func howtoLinesTor() []string {
        return []string{
                "1. Install the SimpleX Chat app on every device you want to connect -",
                "   from Google Play (Android), the App Store (iPhone / iPad) or",
                "   simplex.chat (every device, computers included).",
                "2. The app has no built-in Tor switch - it needs Tor running beside it:",
                "     PHONE (Android / iPhone):",
                "       a. Install Orbot (F-Droid / Play Store / App Store).",
                "       b. Open Orbot and press Start. That is all - the app connects",
                "          through it, nothing to configure.",
                "     PC (Windows / Linux / macOS):",
                "       a. Download the Tor Browser from torproject.org and install it.",
                "       b. Keep the Tor Browser OPEN while you chat (minimized is fine).",
                "       c. In the SimpleX app: Settings → Network & servers → SOCKS proxy -",
                "          switch it on and enter host 127.0.0.1, port 9150.",
                "       d. Alternative: run Tor itself in the background and use port",
                "          9050 instead: Windows - the Tor Expert Bundle (torproject.org,",
                "          unzip and run tor.exe); Linux: sudo apt install tor.",
                "       e. On the very machine that runs sxops you can skip all of the",
                "          above and point the app's SOCKS proxy at sxops' own Tor -",
                "          its SOCKS port right now:",
                "          {{SOCKS-PORT}}",
                "3. Paste your addresses from the \"Server addresses\" card above into the",
                "   app (Settings → SimpleX servers - or press the QR button and scan):",
                "     smp://    your chat server - it carries the messages.",
                "     xftp://   your file server - it carries pictures and file transfers.",
                "   The app asks to use each server you add - confirm, and new chats and",
                "   file transfers run through YOUR server.",
                "4. The chat relay is optional (it hosts groups and channels on your",
                "   server): paste its https link under Settings → Network & servers →",
                "   Chat relay. Desktop apps can also add the ntf:// address under",
                "   Notification servers (push notifications while the app is closed).",
                "5. Check: the app's server test must pass for every address you pasted.",
                "   \"Test failed at step Connect\" means the app cannot reach the server:",
                "   Orbot / the Tor Browser is not running, or the proxy port is wrong.",
                "   The servers themselves are fine - `sxops check` proves it.",
                "6. To chat with someone, share your simplex.chat contact link as usual -",
                "   messages now travel through YOUR server instead of the SimpleX-hosted ones.",
        }
}

// howToUpdate refreshes the live SOCKS port line (the client SOCKS port
// is shown: in single-hop mode the service instance has no SOCKS port at
// all).
func (v *dashView) howToUpdate() {
        text := "     127.0.0.1:9050  ← sxops' Tor SOCKS port right now"
        if p := v.c.eng.Tor.ClientSocksPort(); p > 0 {
                text = fmt.Sprintf("     127.0.0.1:%d  ← sxops' Tor SOCKS port right now", p)
        } else if !v.c.eng.Tor.Running() {
                text = "     (start Tor in sxops to see the SOCKS port)"
        }
        if v.socksLbl.Text != text {
                v.socksLbl.SetText(text)
        }
}
