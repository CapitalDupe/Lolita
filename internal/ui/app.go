// Package ui implements the sxops graphical console (Fyne).
//
// The console is designed for users with zero server experience: the
// first run walks through a one-button setup wizard, then the dashboard
// shows live status, the server addresses with QR codes, and simple
// admin tools.
package ui

import (
        "sync"
        "time"

        "fyne.io/fyne/v2"
        "fyne.io/fyne/v2/app"
        "fyne.io/fyne/v2/container"
        "fyne.io/fyne/v2/dialog"
        "fyne.io/fyne/v2/theme"
        "fyne.io/fyne/v2/widget"

        "sxops/internal/core"
)

// guiLog is an InstallLog that buffers lines for the wizard progress.
type guiLog struct {
        mu    sync.Mutex
        lines []string
        dirty bool
}

func (g *guiLog) Step(msg string)   { g.add("» " + msg) }
func (g *guiLog) Detail(msg string) { g.add("   " + msg) }

func (g *guiLog) add(l string) {
        g.mu.Lock()
        g.lines = append(g.lines, l)
        if len(g.lines) > 500 {
                g.lines = g.lines[len(g.lines)-500:]
        }
        g.dirty = true
        g.mu.Unlock()
}

func (g *guiLog) snapshot(reset bool) []string {
        g.mu.Lock()
        defer g.mu.Unlock()
        out := make([]string, len(g.lines))
        copy(out, g.lines)
        if reset {
                g.dirty = false
        }
        return out
}

func (g *guiLog) wasUpdated() bool {
        g.mu.Lock()
        defer g.mu.Unlock()
        d := g.dirty
        g.dirty = false
        return d
}

// console is the root of the GUI.
type console struct {
        app     fyne.App
        win     fyne.Window
        version string

        eng  *core.Engine
        glog *guiLog

        stack   *fyne.Container // view layers (all hidden except active)
        navBtns []*widget.Button
        viewIdx map[string]int

        dash *dashView
        wiz  *wizardView
        adm  *adminView
        logs *logsView
        torp *torView
        upd  *updatesView
        set  *settingsView

        stopPoll chan struct{}

        // stopping guards the close-window "Stop servers" flow while the
        // servers are being stopped (v1.6.32): re-clicking X or choosing
        // Stop again must not start a second StopAll or close the window
        // out from under the running stop.
        stopping bool
}

// Run starts the graphical console.
func Run(version string) {
        // Stock fyne behaviour: app.NewWithID creates an empty
        // ~/.cache/fyne/sxops tree (its notification-scheduler cache -
        // sxops never schedules notifications, so it stays empty). That
        // is fyne's own doing and v1.6.46 deliberately leaves it alone:
        // the operator reviewed the redirect-and-sweep approach and
        // asked for plain stock behaviour instead. Do not re-add any
        // cache suppression here.
        a := app.NewWithID("sxops")
        // read-only (disabled) entries at full text contrast - the stock
        // dim-grey disabled color made addresses and logs hard to read
        installReadableTheme(a)
        w := a.NewWindow("sxops — SimpleX Server Console")
        w.Resize(fyne.NewSize(1020, 700))

        c := &console{app: a, win: w, version: version, glog: &guiLog{}}
        eng, err := core.NewEngine(c.glog)
        if err != nil {
                dialog.NewError(err, w).Show()
                w.SetContent(widget.NewLabel("sxops could not start: " + err.Error()))
                w.ShowAndRun()
                return
        }
        c.eng = eng

        c.buildViews()
        c.buildShell()

        // first run -> wizard, otherwise dashboard
        if eng.State.Get().Provision {
                c.show("dashboard")
        } else {
                c.show("wizard")
        }

        go c.pollLoop()

        w.SetCloseIntercept(func() { c.confirmClose() })
        w.ShowAndRun()
}

// buildViews creates all view layers.
func (c *console) buildViews() {
        c.dash = newDashView(c)
        c.wiz = newWizardView(c)
        c.adm = newAdminView(c)
        c.logs = newLogsView(c)
        c.torp = newTorView(c)
        c.upd = newUpdatesView(c)
        c.set = newSettingsView(c)

        layers := []fyne.CanvasObject{
                c.wiz.root,
                c.dash.root,
                c.adm.root,
                c.logs.root,
                c.torp.root,
                c.upd.root,
                c.set.root,
        }
        c.viewIdx = map[string]int{
                "wizard": 0, "dashboard": 1, "admin": 2, "logs": 3, "tor": 4, "updates": 5, "settings": 6,
        }
        c.stack = container.NewStack(layers...)
        for i, o := range layers {
                if i > 0 {
                        o.Hide()
                }
        }
}

// buildShell assembles the window content: left nav + view stack.
func (c *console) buildShell() {
        title := widget.NewLabel("sxops")
        title.TextStyle = fyne.TextStyle{Bold: true}
        sub := widget.NewLabel("SimpleX server console")
        sub.TextStyle = fyne.TextStyle{Italic: true}

        nav := func(name, label string, icon fyne.Resource) *widget.Button {
                var b *widget.Button
                b = widget.NewButtonWithIcon(label, icon, func() { c.show(name) })
                b.Alignment = widget.ButtonAlignLeading
                c.navBtns = append(c.navBtns, b)
                return b
        }

        navBox := container.NewVBox(
                container.NewVBox(title, sub),
                widget.NewSeparator(),
                nav("dashboard", "Dashboard", theme.HomeIcon()),
                nav("admin", "Admin tools", theme.SettingsIcon()),
                nav("logs", "Logs", theme.DocumentIcon()),
                nav("tor", "Tor network", theme.ComputerIcon()),
                nav("updates", "Updates", theme.DownloadIcon()),
                nav("settings", "Settings", theme.MenuIcon()),
        )
        ver := widget.NewLabel("v" + c.version[len("sxops "):])
        ver.TextStyle = fyne.TextStyle{Italic: true}
        back := widget.NewButtonWithIcon("Set up again", theme.MediaReplayIcon(), func() {
                c.show("wizard")
        })
        side := container.NewBorder(
                navBox,
                container.NewVBox(widget.NewSeparator(), ver, back),
                nil, nil,
        )

        c.win.SetContent(container.NewBorder(nil, nil, container.NewPadded(side), nil,
                container.NewPadded(c.stack)))
}

// show switches to a named view and refreshes it.
func (c *console) show(name string) {
        idx, ok := c.viewIdx[name]
        if !ok {
                return
        }
        for i, o := range c.stack.Objects {
                if i == idx {
                        o.Show()
                } else {
                        o.Hide()
                }
        }
        for i, b := range c.navBtns {
                if i == idx-1 { // nav excludes the wizard layer
                        b.Importance = widget.HighImportance
                } else {
                        b.Importance = widget.MediumImportance
                }
                b.Refresh()
        }
        c.stack.Refresh()
        c.refreshActive(name)
}

// activeView returns the name of the visible view.
func (c *console) activeView() string {
        for name, i := range c.viewIdx {
                if len(c.stack.Objects) > i && c.stack.Objects[i].Visible() {
                        return name
                }
        }
        return "dashboard"
}

// refreshActive triggers an immediate refresh of the visible view.
func (c *console) refreshActive(name string) {
        switch name {
        case "dashboard":
                go c.dash.refresh()
        case "admin":
                go c.adm.refresh()
        case "logs":
                go c.logs.refresh()
        case "tor":
                go c.torp.refresh()
        case "updates":
                go c.upd.refresh()
        case "settings":
                go c.set.refresh()
        }
}

// pollLoop refreshes statuses in the background.
func (c *console) pollLoop() {
        t := time.NewTicker(4 * time.Second)
        defer t.Stop()
        for {
                select {
                case <-t.C:
                        name := c.activeView()
                        // cheap status poll for dashboard/tor chips
                        statuses := c.eng.Statuses()
                        torSt, torOK := c.eng.Backend.BootstrapStatus()
                        fyne.Do(func() {
                                c.dash.updateStatuses(statuses, torSt, torOK)
                                c.torp.updateStatus(statuses, torSt, torOK)
                        })
                        switch name {
                        case "logs":
                                fyne.Do(func() { c.logs.refresh() })
                        case "admin":
                                // admin refresh happens on demand (buttons)
                        }
                        // wizard log tail
                        if c.wiz != nil && c.wiz.running {
                                if c.glog.wasUpdated() {
                                        lines := c.glog.snapshot(false)
                                        fyne.Do(func() { c.wiz.appendLog(lines) })
                                }
                        }
                case <-c.stopPoll:
                        return
                }
        }
}

// confirmClose asks whether servers should keep running in the
// background; children are detachable so both choices are safe.
func (c *console) confirmClose() {
        if c.stopping {
                // already stopping from an earlier click - the window
                // closes itself when the stop completes
                return
        }
        anyRunning := false
        for _, st := range c.eng.Statuses() {
                if st.Running {
                        anyRunning = true
                        break
                }
        }
        if !anyRunning {
                c.win.SetCloseIntercept(nil)
                c.win.Close()
                return
        }
        doClose := func(keep bool) {
                if keep {
                        c.win.SetCloseIntercept(nil)
                        c.win.Close()
                        return
                }
                // v1.6.32: STOP BEFORE CLOSE. The old flow fired
                // StopAll(true) in a fire-and-forget goroutine and
                // closed the window right away - the goroutine died
                // with the app process the moment main returned, and
                // the stops were sequential (relay, SMP, XFTP, NTF,
                // Tor), so everything after SMP was left running - the
                // XFTP server in the operator's report. Now the window
                // stays open under a progress dialog until every
                // server has actually exited (StopAll stops them in
                // parallel, so this is as fast as the slowest one -
                // normally a couple of seconds), with a watchdog that
                // closes anyway should anything hang.
                c.stopping = true
                done := make(chan struct{})
                d := busyDialog(c.win, "Stopping servers",
                        "Stopping your SimpleX servers before closing…", func() {
                                c.eng.StopAll(true)
                                close(done)
                        })
                d.Show()
                go func() {
                        select {
                        case <-done:
                        case <-time.After(75 * time.Second):
                        }
                        fyne.Do(func() {
                                d.Hide()
                                c.win.SetCloseIntercept(nil)
                                c.win.Close()
                        })
                }()
        }
        keep := widget.NewButton("Keep running", func() { doClose(true) })
        keep.Importance = widget.HighImportance
        stop := widget.NewButton("Stop servers", func() { doClose(false) })
        stop.Importance = widget.DangerImportance
        msg := widget.NewLabel(
                "Your SimpleX servers are still running.\n\n" +
                        "Keep them running in the background (they keep working\n" +
                        "even while sxops is closed), or stop them now?")
        d := dialog.NewCustomWithoutButtons("Servers are running", container.NewVBox(
                msg,
                container.NewHBox(keep, stop),
        ), c.win)
        d.Show()
}
