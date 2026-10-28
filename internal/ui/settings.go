package ui

import (
        "context"
        "os/exec"
        "runtime"
        "strings"
        "time"

        "fyne.io/fyne/v2"
        "fyne.io/fyne/v2/container"
        "fyne.io/fyne/v2/dialog"
        "fyne.io/fyne/v2/theme"
        "fyne.io/fyne/v2/widget"

        "sxops/internal/core"
)

// settingsView holds preferences and general information (data folder,
// version). Software updates live in their own section.
type settingsView struct {
        c    *console
        root fyne.CanvasObject

        relayName  *widget.Entry
        usePw      *widget.Check
        pw         *widget.Entry
        publicHost *widget.Entry
        infoPages  *widget.Check
        serverStats *widget.Select
        singleHop  *widget.Check
        ntfEnable  *widget.Check
        xftpExpiry *widget.Select
        xftpQuota  *widget.Select

        saveBtn *widget.Button
}

// XFTP file expiry choices (stored in hours). 48h ("2 days") is the
// upstream default; the server always expires files - there is no
// "never" mode - so the choices are plain day counts.
var xftpExpiryChoices = []string{"1 day", "2 days", "3 days", "7 days", "14 days", "30 days"}

var xftpExpiryHoursOf = map[string]int{
        "1 day": 24, "2 days": 48, "3 days": 72,
        "7 days": 168, "14 days": 336, "30 days": 720,
}

// XFTP storage quota choices (GB); "Off (no limit)" (0) is the
// default since v1.6.8 - the file server then keeps as many files as
// the disk allows.
var xftpQuotaChoices = []string{"Off (no limit)", "5 GB", "10 GB", "25 GB", "50 GB", "100 GB"}

var xftpQuotaGBOf = map[string]int{
        "Off (no limit)": 0, "5 GB": 5, "10 GB": 10, "25 GB": 25, "50 GB": 50, "100 GB": 100,
}

// v1.6.39: the daily-statistics selector. The upstream switch is
// [STORE_LOG] log_stats per server INI - on = the server collects
// daily aggregate totals (messages and chats for SMP, files for XFTP)
// into LOCAL log files on this computer (smp-server-stats.daily.log /
// file-server-stats.daily.log). The numbers are never published: the
// public info page renders only a "Stats enabled" yes/no line in its
// Configuration section (confirmed on the official servers' pages).
// Off by default.
var serverStatsChoices = []string{"Off", "Yes (SMP)", "Yes (XFTP)", "Both (SMP + XFTP)"}

var serverStatsValueOf = map[string]string{
        "Off": "off", "Yes (SMP)": "smp", "Yes (XFTP)": "xftp", "Both (SMP + XFTP)": "both",
}

var serverStatsLabelOf = map[string]string{
        "off": "Off", "smp": "Yes (SMP)", "xftp": "Yes (XFTP)", "both": "Both (SMP + XFTP)",
}

// serverStatsLabel maps a persisted value to the select's choice
// (unknown and empty values render as "Off" - the default).
func serverStatsLabel(v string) string {
        if l, ok := serverStatsLabelOf[v]; ok {
                return l
        }
        return "Off"
}

// serverStatsSettingLabel is the selector's caption (v1.6.39,
// corrected: the totals are collected into local logs, so it says
// collect, not show-on-the-page). The operator tried a "Server stats
// (Log):" rename in v1.6.40 and reverted it - this stays as it was.
func serverStatsSettingLabel() string { return "Collect daily server statistics:" }

// serverStatsNote is the explanation above the log file paths (v1.6.40:
// the note now says exactly WHERE the daily totals are saved - the two
// monospace path lines shown right below it, no other UI was added).
func serverStatsNote() string {
        return "Statistics are DAILY TOTALS only (messages and chats for SMP, files for XFTP) -\n" +
                "no content, no user identities, no addresses. When on, the totals are\n" +
                "written once a day to these LOCAL log files on this computer (and nowhere else):"
}

// serverStatsNoteTail is the explanation below the log file paths.
// v1.6.41: the Save button sits BELOW this note (bottom of the "My
// server" card), so the wording says below, not above.
func serverStatsNoteTail() string {
        return "The public info page only shows a \"stats enabled\" line, never the numbers.\n" +
                "Applies when you press Save settings below - the affected servers restart briefly."
}

// statsPathLabel renders one daily-stats log file path under the
// stats note (v1.6.40). Monospace and character-wrapped
// (TextWrapBreak) so a long path can never stretch the window - the
// same treatment the data folder path gets in this view.
func statsPathLabel(prefix, path string) *widget.Label {
        l := widget.NewLabel(prefix + path)
        l.TextStyle = fyne.TextStyle{Monospace: true}
        l.Wrapping = fyne.TextWrapBreak
        return l
}

// xftpExpiryLabel picks the choice closest to a persisted hour count
// (state written by hand may hold a value outside the list).
func xftpExpiryLabel(hours int) string {
        best, bestDiff := "2 days", absInt(hours-48)
        for _, c := range xftpExpiryChoices {
                if d := absInt(hours - xftpExpiryHoursOf[c]); d < bestDiff {
                        best, bestDiff = c, d
                }
        }
        return best
}

// xftpQuotaLabel picks the choice matching a persisted GB count
// (0 or negative = off; other off-list values snap to the closest
// choice, so hand-edited states still render something sensible).
func xftpQuotaLabel(gb int) string {
        if gb <= 0 {
                return "Off (no limit)"
        }
        best, bestDiff := "10 GB", absInt(gb-10)
        for _, c := range xftpQuotaChoices {
                if d := absInt(gb - xftpQuotaGBOf[c]); d < bestDiff {
                        best, bestDiff = c, d
                }
        }
        return best
}

func absInt(n int) int {
        if n < 0 {
                return -n
        }
        return n
}

func newSettingsView(c *console) *settingsView {
        v := &settingsView{c: c}
        s := c.eng.State.Settings()

        v.relayName = widget.NewEntry()
        v.relayName.SetText(s.RelayName)

        v.pw = widget.NewPasswordEntry()
        if s.PasswordProtect {
                v.pw.SetText(s.QueuePassword)
        }
        v.usePw = widget.NewCheck("Protect with a password (recommended)", func(ok bool) {
                if ok {
                        v.pw.Show()
                } else {
                        v.pw.Hide()
                }
        })
        v.usePw.SetChecked(s.PasswordProtect)
        if !s.PasswordProtect {
                v.pw.Hide()
        }

        v.publicHost = widget.NewEntry()
        v.publicHost.SetText(s.PublicHost)

        v.infoPages = widget.NewCheck("Show server web pages (info + link invite pages for browsers)", nil)
        v.infoPages.SetChecked(s.EnableInfoPages)
        v.serverStats = widget.NewSelect(serverStatsChoices, nil)
        v.serverStats.SetSelected(serverStatsLabel(s.ServerStats))
        v.singleHop = widget.NewCheck("Faster onion routing (single-hop, less anonymous) - changing this regenerates your onion addresses", nil)
        v.singleHop.SetChecked(s.SingleHopOnion)
        v.ntfEnable = widget.NewCheck("Run the notification server", nil)
        v.ntfEnable.SetChecked(s.EnableNTF)

        v.xftpExpiry = widget.NewSelect(xftpExpiryChoices, nil)
        v.xftpExpiry.SetSelected(xftpExpiryLabel(s.XFTPExpireHours))
        v.xftpQuota = widget.NewSelect(xftpQuotaChoices, nil)
        v.xftpQuota.SetSelected(xftpQuotaLabel(s.XFTPQuotaGB))

        v.saveBtn = widget.NewButtonWithIcon("Save settings", theme.DocumentSaveIcon(), func() { v.save() })
        v.saveBtn.Importance = widget.HighImportance

        pwNote := widget.NewLabel("The password protects who can create new chats (queues) on your server.\n" +
                "It becomes part of the address you share - changes apply the next time you run setup.")
        pwNote.Wrapping = fyne.TextWrapWord
        pwNote.TextStyle = fyne.TextStyle{Italic: true}

        hostNote := widget.NewLabel("Leave empty to use the Tor onion address only (recommended - works everywhere,\nno domain needed). Fill in only if you also own a public domain or static IP.")
        hostNote.Wrapping = fyne.TextWrapWord
        hostNote.TextStyle = fyne.TextStyle{Italic: true}

        statsNote := widget.NewLabel(serverStatsNote())
        statsNote.Wrapping = fyne.TextWrapWord
        statsNote.TextStyle = fyne.TextStyle{Italic: true}

        // v1.6.40: the exact files the daily totals go to.
        statsSMPPath := statsPathLabel("SMP: ", c.eng.Paths.StatsLogFile(core.KindSMP))
        statsXFTPPath := statsPathLabel("XFTP: ", c.eng.Paths.StatsLogFile(core.KindXFTP))
        statsTail := widget.NewLabel(serverStatsNoteTail())
        statsTail.Wrapping = fyne.TextWrapWord
        statsTail.TextStyle = fyne.TextStyle{Italic: true}

        xftpNote := widget.NewLabel("Files sent in chats are stored on your server until they expire - anyone who did not\ndownload in time simply asks the sender to send again (the apps make that easy).\nBoth apply when you press Save settings above: the file server restarts briefly.")
        xftpNote.Wrapping = fyne.TextWrapWord
        xftpNote.TextStyle = fyne.TextStyle{Italic: true}

        dataDir := widget.NewLabel(c.eng.Paths.Root)
        dataDir.TextStyle = fyne.TextStyle{Monospace: true}
        dataDir.Wrapping = fyne.TextWrapBreak
        openDir := widget.NewButtonWithIcon("Open data folder", theme.FolderOpenIcon(), func() { openFolder(v.c) })

        v.root = container.NewVScroll(container.NewVBox(
                card("My server", "", container.NewVBox(
                        widget.NewLabel("Relay display name (what contacts see):"),
                        v.relayName,
                        v.usePw,
                        v.pw,
                        pwNote,
                        widget.NewSeparator(),
                        v.infoPages,
                        widget.NewLabel(serverStatsSettingLabel()),
                        v.serverStats,
                        statsNote,
                        statsSMPPath,
                        statsXFTPPath,
                        statsTail,
                        v.singleHop,
                        widget.NewSeparator(),
                        widget.NewLabel("Advanced: public host (optional)"),
                        v.publicHost,
                        hostNote,
                        widget.NewSeparator(),
                        v.saveBtn,
                )),
                card("File server (XFTP)", "Files shared in chats are stored on your server until they expire.", container.NewVBox(
                        widget.NewLabel("Delete stored files after:"),
                        v.xftpExpiry,
                        widget.NewLabel("Total storage space for files:"),
                        v.xftpQuota,
                        widget.NewLabel("\"Off\" (the default) keeps files until they expire with no size cap;\na quota limits the total size of stored files - uploads fail once it is full."),
                        xftpNote,
                )),
                card("Notification server (optional)", "Wakes up mobile apps when messages arrive while they sleep in the background.", container.NewVBox(
                        v.ntfEnable,
                        widget.NewLabel("sxops sets up everything it needs automatically - including a private\nPostgreSQL database that runs under your own user (no root, no system service,\nreachable only through its own local socket)."),
                        widget.NewLabel("Honest limits: Android and desktop apps do not need it, and the official\niOS app always uses SimpleX's own notification server (Apple's push rules - only a\nself-compiled iOS app could use yours). Enable it for completeness, or skip it -\nthe official infrastructure stays available either way, and enabling changes\nnothing about message privacy: push tokens are anonymous."),
                )),
                card("Data folder", "Everything (programs, keys, message database) lives here.", container.NewVBox(
                        dataDir, openDir)),
                card("About", "", richLines([]string{
                        "sxops " + strings.TrimPrefix(v.c.version, "sxops "),
                        "A friendly console for your own SimpleX Chat servers.",
                        "SimpleX is a private messenger - your server relays messages without\nknowing who sends or receives them.",
                        "New SimpleX versions are installed from the Updates section.",
                })),
        ))
        return v
}

// refresh syncs widgets with persisted settings (called on tab open).
func (v *settingsView) refresh() {
        s := v.c.eng.State.Settings()
        fyne.Do(func() {
                v.relayName.SetText(s.RelayName)
                v.usePw.SetChecked(s.PasswordProtect)
                v.pw.SetText(s.QueuePassword)
                v.publicHost.SetText(s.PublicHost)
                v.infoPages.SetChecked(s.EnableInfoPages)
                v.serverStats.SetSelected(serverStatsLabel(s.ServerStats))
                v.singleHop.SetChecked(s.SingleHopOnion)
                v.ntfEnable.SetChecked(s.EnableNTF)
                v.xftpExpiry.SetSelected(xftpExpiryLabel(s.XFTPExpireHours))
                v.xftpQuota.SetSelected(xftpQuotaLabel(s.XFTPQuotaGB))
        })
}

// save persists the form.
func (v *settingsView) save() {
        old := v.c.eng.State.Settings()
        s := old
        s.RelayName = strings.TrimSpace(v.relayName.Text)
        if s.RelayName == "" {
                s.RelayName = "My Relay"
        }
        s.PasswordProtect = v.usePw.Checked
        s.QueuePassword = v.pw.Text
        s.PublicHost = strings.TrimSpace(v.publicHost.Text)
        s.EnableInfoPages = v.infoPages.Checked
        s.ServerStats = serverStatsValueOf[v.serverStats.Selected]
        s.SingleHopOnion = v.singleHop.Checked
        ntfChanged := s.EnableNTF != v.ntfEnable.Checked
        s.EnableNTF = v.ntfEnable.Checked
        s.XFTPExpireHours = xftpExpiryHoursOf[v.xftpExpiry.Selected]
        s.XFTPQuotaGB = xftpQuotaGBOf[v.xftpQuota.Selected]

        singleHopChanged := s.SingleHopOnion != old.SingleHopOnion && v.anyOnionHost()
        publicHostChanged := s.PublicHost != old.PublicHost || s.PublicPort != old.PublicPort
        xftpChanged := s.XFTPExpireHours != old.XFTPExpireHours || s.XFTPQuotaGB != old.XFTPQuotaGB
        infoPagesChanged := s.EnableInfoPages != old.EnableInfoPages
        statsChanged := s.ServerStats != old.ServerStats
        // v1.6.39: only the servers whose stats coverage actually
        // flipped are restarted - e.g. Off -> Yes (XFTP) must not touch
        // the chat server at all (its INI is unchanged).
        smpStatsChanged := s.ServerStatsEnabled(core.KindSMP) != old.ServerStatsEnabled(core.KindSMP)
        xftpStatsChanged := s.ServerStatsEnabled(core.KindXFTP) != old.ServerStatsEnabled(core.KindXFTP)

        if err := v.c.eng.State.SetSettings(s); err != nil {
                showError(v.c.win, err)
                return
        }

        if publicHostChanged {
                // advertised hosts changed: re-patch the server INIs now
                // (the SMP web certificate follows the new host)
                for _, k := range []core.Kind{core.KindSMP, core.KindXFTP, core.KindNTF} {
                        if v.c.eng.State.Server(k).Installed {
                                _ = v.c.eng.PatchIni(k)
                        }
                }
                if v.c.eng.State.Server(core.KindSMP).Installed {
                        v.c.eng.SyncSMPWebPage()
                }
        }

        if ntfChanged {
                // enabling/disabling the notification server is a small setup
                // operation (private PostgreSQL + binary + onion service)
                v.confirmNTF(s.EnableNTF)
                return
        }
        if singleHopChanged {
                // Tor cannot reuse hidden-service keys across anonymous /
                // non-anonymous mode - the onion addresses MUST be regenerated.
                v.confirmSingleHop(s)
                return
        }
        if infoPagesChanged || statsChanged {
                // The info/link pages AND the daily statistics are all read
                // by the servers at start - re-patch the INIs and restart
                // the AFFECTED servers briefly in the background, like the
                // file server settings. v1.6.39: the stats choice can cover
                // the file server too, and each server is reloaded only when
                // its own coverage flipped. v1.6.41: the web-pages toggle is
                // shared by BOTH servers - turning it off must also take the
                // XFTP info web page down ([WEB] http in file-server.ini and
                // the onion virtual-80 mapping), so the file server reloads
                // on a pages flip as well (the SMP web certificate is SMP-
                // only and keeps its own sync).
                // Turning pages ON also needs a running Tor restarted so the
                // onion service maps virtual port 443 to the HTTPS page
                // (a running Tor never re-reads its torrc; the onion keys
                // persist, the address stays).
                smpInstalled := v.c.eng.State.Server(core.KindSMP).Installed
                xftpInstalled := v.c.eng.State.Server(core.KindXFTP).Installed
                reloadSMP := infoPagesChanged || smpStatsChanged
                reloadXFTP := infoPagesChanged || xftpStatsChanged
                if (smpInstalled && reloadSMP) || (xftpInstalled && reloadXFTP) {
                        _ = v.c.eng.Tor.GenerateTorrc()
                        busyDialog(v.c.win, "Server settings", "Applying server settings...", func() {
                                var err error
                                if smpInstalled && reloadSMP {
                                        err = v.c.eng.ReloadServerConfig(core.KindSMP)
                                }
                                if err == nil && xftpInstalled && reloadXFTP {
                                        err = v.c.eng.ReloadServerConfig(core.KindXFTP)
                                }
                                // v1.6.41: a pages flip changes the torrc for
                                // BOTH servers (the onion virtual-80 mapping
                                // is EnableInfoPages-gated for each), so a
                                // running Tor is restarted on either direction
                                // of the toggle - not only when pages turn on.
                                if err == nil && infoPagesChanged && v.c.eng.Tor.Running() {
                                        ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
                                        defer cancel()
                                        err = v.c.eng.Tor.Start(ctx)
                                }
                                fyne.Do(func() {
                                        if err != nil {
                                                showError(v.c.win, err)
                                                return
                                        }
                                        v.savedFeedback()
                                })
                        }).Show()
                        return
                }
        }
        if xftpChanged && v.c.eng.State.Server(core.KindXFTP).Installed {
                // Tor config depends on some settings; regenerate torrc now,
                // then re-patch the file server INI and restart it (briefly)
                // in the background - it reads expiry and quota at start.
                _ = v.c.eng.Tor.GenerateTorrc()
                busyDialog(v.c.win, "File server settings", "Applying file server settings...", func() {
                        err := v.c.eng.ReloadServerConfig(core.KindXFTP)
                        fyne.Do(func() {
                                if err != nil {
                                        showError(v.c.win, err)
                                        return
                                }
                                v.savedFeedback()
                        })
                }).Show()
                return
        }
        // Tor config depends on some settings; regenerate torrc now.
        _ = v.c.eng.Tor.GenerateTorrc()

        v.savedFeedback()
}

// savedFeedback flashes the save button.
func (v *settingsView) savedFeedback() {
        v.saveBtn.SetText("Saved!")
        time.AfterFunc(1500*time.Millisecond, func() {
                fyne.Do(func() { v.saveBtn.SetText("Save settings") })
        })
}

// anyOnionHost reports whether any installed server already published an
// onion address (switching single-hop then REQUIRES regenerating them).
func (v *settingsView) anyOnionHost() bool {
        for _, k := range []core.Kind{core.KindSMP, core.KindXFTP, core.KindNTF} {
                if v.c.eng.State.Server(k).OnionHost != "" {
                        return true
                }
        }
        return false
}

// confirmSingleHop asks before applying a single-hop change (the onion
// addresses change with it - that is Tor's requirement, not a choice),
// then hands over to the engine.
func (v *settingsView) confirmSingleHop(s core.Settings) {
        msg := widget.NewLabel(
                "Switch single-hop onion routing?\n\n" +
                        "Tor requires fresh onion-service keys when switching between\n" +
                        "single-hop and 3-hop mode, so your onion addresses CHANGE -\n" +
                        "the old ones stop working and the new ones appear after Tor\n" +
                        "connects again (Tor network tab - Connect).\n\n" +
                        "Single-hop = lower latency, but the server side loses its\n" +
                        "location anonymity.")
        msg.Wrapping = fyne.TextWrapWord
        d := dialog.NewCustomConfirm("Change single-hop mode", "Apply change", "Cancel", msg, func(ok bool) {
                if !ok {
                        v.singleHop.SetChecked(!s.SingleHopOnion) // revert the checkbox
                        _ = v.c.eng.Tor.GenerateTorrc()
                        return
                }
                go func() {
                        warn, err := v.c.eng.HandleSingleHopChange()
                        fyne.Do(func() {
                                if err != nil {
                                        showError(v.c.win, err)
                                        return
                                }
                                if warn != "" {
                                        l := widget.NewLabel(warn)
                                        l.Wrapping = fyne.TextWrapWord
                                        dialog.NewCustom("Single-hop mode changed", "Close", l, v.c.win).Show()
                                }
                        })
                }()
        }, v.c.win)
        d.Show()
}

// confirmNTF asks before applying the notification server change, then
// runs the whole enable/disable flow in a busy dialog (downloading the
// server, setting up its private PostgreSQL database, publishing its
// onion service and starting it - or stopping all of that).
func (v *settingsView) confirmNTF(enable bool) {
        var msg string
        if enable {
                msg = "Turn ON the notification server?\n\n" +
                        "sxops will download the notification server program (about 15 MB),\n" +
                        "set up its private PostgreSQL database (a one-time initdb under your\n" +
                        "data folder), publish its onion address (Tor restarts briefly - the\n" +
                        "other onion addresses are unchanged) and start the server.\n\n" +
                        "Its ntf:// address appears on the Dashboard when done.\n\n" +
                        "PostgreSQL must be installed on this computer (the binaries are enough -\n" +
                        "sxops runs its own private instance; the system service is never touched)."
        } else {
                msg = "Turn OFF the notification server?\n\n" +
                        "The server and its private PostgreSQL database are stopped.\n" +
                        "Everything is kept (database, keys, onion address), so turning it\n" +
                        "back on later is quick and the address does not change."
        }
        l := widget.NewLabel(msg)
        l.Wrapping = fyne.TextWrapWord
        d := dialog.NewCustomConfirm("Notification server", "Apply", "Cancel", l, func(ok bool) {
                if !ok {
                        v.ntfEnable.SetChecked(!enable) // revert the checkbox
                        return
                }
                busy := "Stopping the notification server..."
                if enable {
                        busy = "Setting up the notification server..."
                }
                busyD := busyDialog(v.c.win, "Notification server", busy, func() {
                        var err error
                        if enable {
                                ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
                                defer cancel()
                                err = v.c.eng.EnableNTFServer(ctx)
                        } else {
                                err = v.c.eng.DisableNTFServer()
                        }
                        fyne.Do(func() {
                                if err != nil {
                                        showError(v.c.win, err)
                                        return
                                }
                                var done *widget.Label
                                if enable {
                                        done = widget.NewLabel("The notification server is running - its ntf:// address is on the Dashboard.\nDesktop apps can use it via Settings - Network & servers - Notification servers.")
                                } else {
                                        done = widget.NewLabel("The notification server is stopped. Its data was kept -\nturning it back on is quick and keeps the same address.")
                                }
                                done.Wrapping = fyne.TextWrapWord
                                dialog.NewCustom("Notification server", "Close", done, v.c.win).Show()
                        })
                })
                busyD.Show()
        }, v.c.win)
        d.Show()
}

// openFolder opens the data folder in the desktop file manager.
func openFolder(c *console) {
        dir := c.eng.Paths.Root
        var err error
        switch runtime.GOOS {
        case "darwin":
                err = exec.Command("open", dir).Start()
        default:
                err = exec.Command("xdg-open", dir).Start()
        }
        if err != nil {
                l := widget.NewLabel("Could not open the file manager automatically.\nThe folder is at:\n" + dir)
                l.Wrapping = fyne.TextWrapWord
                dialog.NewCustom("Data folder", "Close",
                        container.NewVBox(l, copyButton(c.win, func() string { return dir })), c.win).Show()
        }
}
