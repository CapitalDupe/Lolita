package ui

import (
        "fmt"
        "sort"
        "strconv"
        "strings"
        "time"

        "fyne.io/fyne/v2"
        "fyne.io/fyne/v2/container"
        "fyne.io/fyne/v2/theme"
        "fyne.io/fyne/v2/widget"

        "sxops/internal/core"
)

// adminView exposes the admin tools that talk to the servers' control
// ports: live stats, connected clients, server info and client
// moderation (ban / unban / delete).
type adminView struct {
        c    *console
        root fyne.CanvasObject

        sel      *widget.Select
        selected core.Kind

        statLbl  *widget.Label
        statsBox *fyne.Container

        clientsHead *widget.Label
        clientsBox  *fyne.Container
        clientsCnt  *widget.Label

        banIDEntry   *widget.Entry
        banIDLabel   *widget.Label // the entry's left label, switches with the kind
        banResult    *widget.Label
        banBtn       *widget.Button
        unbanBtn     *widget.Button
        banStatusBtn *widget.Button
        banDeleteBtn *widget.Button
        banHead      *widget.Label // card title: Ban a client / Block a file
        banSub       *widget.Label // card copy, switches with the selected kind

        queuesHead *widget.Label
        queuesCnt  *widget.Label
        queuesBox  *fyne.Container
        lastQueues []core.QueueInfo

        bansCnt   *widget.Label
        bansBox   *fyne.Container
        infoEntry *widget.Entry

        gateCard fyne.CanvasObject // server-wide registration gate card (hidden for NTF)
        gateLbl  *widget.Label
        gateBtn  *widget.Button

        // the official report channel: [INFORMATION] complaints contacts
        reportsCard  fyne.CanvasObject // hidden for NTF / relay
        reportsEntry *widget.Entry     // complaints_simplex (chat address)
        reportsEmail *widget.Entry     // complaints_email (optional)
        reportsBtn   *widget.Button    // Save (writes INI, restarts server)
        reportsCur   *widget.Label     // what is configured right now
        reportsHint  *widget.Label     // shown while no address is set

        busy    bool // a stats/clients refresh is in flight
        busyMod bool // a moderation command is in flight
}

func newAdminView(c *console) *adminView {
        v := &adminView{c: c, selected: core.KindSMP}

        v.sel = widget.NewSelect([]string{"SMP server", "XFTP server", "NTF server"}, func(s string) {
                switch s {
                case "SMP server":
                        v.selected = core.KindSMP
                case "XFTP server":
                        v.selected = core.KindXFTP
                case "NTF server":
                        v.selected = core.KindNTF
                }
                v.refresh()
        })
        v.sel.PlaceHolder = "SMP server"
        v.sel.SetSelected("SMP server")

        v.statLbl = widget.NewLabel("Press Refresh to load live statistics.")
        v.statLbl.Wrapping = fyne.TextWrapWord
        v.statsBox = container.NewVBox(v.statLbl)

        v.clientsHead = widget.NewLabel("")
        v.clientsHead.TextStyle = fyne.TextStyle{Bold: true}
        v.clientsCnt = widget.NewLabel("")
        v.clientsCnt.TextStyle = fyne.TextStyle{Italic: true}
        v.clientsBox = container.NewVBox()

        // --- client moderation (ban from server) ---
        v.banIDEntry = widget.NewEntry()
        v.banIDEntry.TextStyle = fyne.TextStyle{Monospace: true}
        v.banIDEntry.Wrapping = fyne.TextWrapOff
        v.banIDEntry.PlaceHolder = "queue / link ID, or a pasted invitation / group link (https:// or simplex:/)"
        v.banIDLabel = widget.NewLabel("Client ID or link:")

        v.banResult = widget.NewLabel("The server's response appears here.")
        v.banResult.Wrapping = fyne.TextWrapWord

        v.banBtn = widget.NewButtonWithIcon("Ban client", theme.CancelIcon(), func() { v.doBan() })
        v.banBtn.Importance = widget.DangerImportance
        v.unbanBtn = widget.NewButtonWithIcon("Unban", theme.MediaPlayIcon(), func() { v.doUnban() })
        v.banStatusBtn = widget.NewButtonWithIcon("Check status", theme.SearchIcon(), func() { v.doStatus() })
        v.banDeleteBtn = widget.NewButtonWithIcon("Delete queue", theme.DeleteIcon(), func() { v.doDelete() })
        v.banDeleteBtn.Importance = widget.DangerImportance

        banForm := container.NewVBox(
                // Border (not HBox): HBox gives the entry only its ~one-character
                // minimum width, rendering the Client ID box unusably tiny.
                // Border stretches it across the full card width.
                container.NewBorder(nil, nil, v.banIDLabel, nil, v.banIDEntry),
                container.NewHBox(
                        container.NewGridWrap(fyne.NewSize(150, v.banBtn.MinSize().Height), v.banBtn),
                        container.NewGridWrap(fyne.NewSize(110, v.unbanBtn.MinSize().Height), v.unbanBtn),
                        container.NewGridWrap(fyne.NewSize(130, v.banStatusBtn.MinSize().Height), v.banStatusBtn),
                        container.NewGridWrap(fyne.NewSize(140, v.banDeleteBtn.MinSize().Height), v.banDeleteBtn),
                ),
                v.banResult,
        )

        v.bansCnt = widget.NewLabel("")
        v.bansCnt.TextStyle = fyne.TextStyle{Italic: true}
        v.bansBox = container.NewVBox()

        // --- the official report channel: [INFORMATION] complaints ---
        // the address users get from the server's HTTPS info page to
        // message the operator about abuse (v1.6.19: it must be a durable
        // contact address - the server refuses one-time links and a
        // refused address stops it from starting)
        v.reportsEntry = widget.NewEntry()
        v.reportsEntry.TextStyle = fyne.TextStyle{Monospace: true}
        // single-line entries must not wrap (Fyne logs "Entry cannot
        // wrap single line" and falls back to truncation); the field
        // scrolls horizontally instead, like the ban ID field
        v.reportsEntry.Wrapping = fyne.TextWrapOff
        v.reportsEntry.PlaceHolder = "https://server/a#… or simplex:/a#…  (your address)"
        v.reportsEmail = widget.NewEntry()
        v.reportsEmail.PlaceHolder = "abuse@example.com (optional)"
        v.reportsCur = widget.NewLabel("")
        v.reportsCur.TextStyle = fyne.TextStyle{Italic: true}
        v.reportsCur.Wrapping = fyne.TextWrapWord
        v.reportsHint = widget.NewLabel("")
        v.reportsHint.TextStyle = fyne.TextStyle{Italic: true}
        v.reportsHint.Wrapping = fyne.TextWrapWord
        v.reportsBtn = widget.NewButtonWithIcon("Save report address", theme.DocumentSaveIcon(), func() { v.doSaveComplaints() })
        reportsForm := container.NewVBox(
                container.NewBorder(nil, nil, widget.NewLabel("Your SimpleX address:"), nil, v.reportsEntry),
                container.NewBorder(nil, nil, widget.NewLabel("Email (optional):"), nil, v.reportsEmail),
                container.NewHBox(container.NewGridWrap(fyne.NewSize(170, v.reportsBtn.MinSize().Height), v.reportsBtn)),
                v.reportsHint,
                v.reportsCur,
        )
        v.reportsCard = card("Abuse reports",
                "How reports work: messages and files are reported to the group's moderators - end-to-end encrypted, they never touch any server. The address below is how users reach YOU: the server publishes it on its HTTPS info page, users copy it into their app and message you. There is no in-app report button for third-party servers yet - that flow is still planned upstream. Paste your durable SimpleX address (profile -> SimpleX address), never a one-time link: the server refuses one-time links and would not start. Both spellings are accepted - the https:// form and the compact simplex:/ form the apps share. Leave it empty and the page shows no contact.",
                reportsForm)

        // --- server-wide registration gate ([AUTH] new_queues/new_files) ---
        // the official lever for turning new registrations back on after a
        // ban dialog checked "also refuse new registrations server-wide".
        v.gateLbl = widget.NewLabel("")
        v.gateLbl.Wrapping = fyne.TextWrapWord
        v.gateBtn = widget.NewButton("", nil)
        v.gateCard = card("New registrations & uploads gate",
                "The official SimpleX servers' own anti-spam lever: while it is off, the chat server refuses to create new chat queues (contact addresses, group invites, new chats) and the file server refuses new file uploads - every existing chat and file keeps working. The chat protocol has no per-client identity, so this server-wide gate is the only official way to stop new connections. Applied with a server restart.",
                container.NewVBox(v.gateLbl, v.gateBtn))

        v.queuesHead = widget.NewLabel("Chat queues on this server")
        v.queuesHead.TextStyle = fyne.TextStyle{Bold: true}
        v.queuesCnt = widget.NewLabel("")
        v.queuesCnt.TextStyle = fyne.TextStyle{Italic: true}
        v.queuesBox = container.NewVBox()

        v.infoEntry = widget.NewMultiLineEntry()
        v.infoEntry.TextStyle = fyne.TextStyle{Monospace: true}
        // wrapping (not TextWrapOff) keeps long server-info lines from
        // stretching the window and lets the entry scroll internally;
        // 12 visible rows give it a real pane (it must NOT sit inside a
        // container.NewVScroll - that collapses it to ~32px)
        v.infoEntry.Wrapping = fyne.TextWrapBreak
        v.infoEntry.SetMinRowsVisible(12)
        v.infoEntry.Disable()
        v.infoEntry.SetText("(server information appears here)")

        refresh := widget.NewButtonWithIcon("Refresh", theme.ViewRefreshIcon(), func() { v.refresh() })
        refresh.Importance = widget.HighImportance

        // "suspend"/"resume" are deliberately absent: the servers parse
        // them but answer "not implemented" (verified against v6.5.0 and
        // stable). The state-save button was removed in v1.6.45: the
        // underlying command only makes the server flush its state to
        // disk - nothing visible changes in the app, so the button
        // looked dead. Offering commands that do nothing would be
        // selling features that do nothing.
        actions := container.NewHBox(refresh)

        // the ban card's title and copy switch with the selected server
        // kind (chat queues vs. files) - updated in setBanCardUI
        v.banHead = widget.NewLabel("Ban a client from the server")
        v.banHead.TextStyle = fyne.TextStyle{Bold: true}
        v.banSub = widget.NewLabel(banCardCopy(core.KindSMP))
        v.banSub.Wrapping = fyne.TextWrapWord
        banCard := container.NewVBox(
                container.NewVBox(v.banHead, v.banSub),
                widget.NewSeparator(),
                container.NewPadded(banForm),
        )

        v.root = container.NewVScroll(container.NewVBox(
                card("Admin tools", "These tools talk to your servers directly over their local control ports - nothing here leaves your machine.",
                        container.NewVBox(
                                container.NewHBox(widget.NewLabel("Server:"), v.sel),
                                actions,
                        )),
                card("Live statistics", "Numbers reported by the server right now.", v.statsBox),
                card("Connected clients", "Live sessions talking to your server right now, with real dates and times in your local time zone (the server reports raw timestamps).", container.NewVBox(v.clientsHead, v.clientsCnt, v.clientsBox)),
                card("", "", container.NewVBox(v.queuesHead,
                        v.queuesCnt,
                        v.queuesBox)),
                banCard,
                v.reportsCard,
                card("Ban list",
                        "Every ban the server holds, read from its store log with the reason and ban notice it recorded - including bans placed outside sxops. A banned client cannot send or receive through the banned queue. The journal carries no dates, so none are shown. Chat-server bans are undone with Unban; file-server blocks delete the stored chunks at block time and cannot be undone.",
                        container.NewVBox(v.bansCnt, v.bansBox)),
                v.gateCard,
                card("Server information", serverInfoCardSubtitle(), v.infoEntry),
                unsupportedNote(),
        ))
        return v
}

// unsupportedNote explains per-server command differences.
func unsupportedNote() fyne.CanvasObject {
        l := widget.NewLabel("Note: the XFTP server understands a smaller set of admin commands\n" +
                "(no client list or status check; file blocks there delete the\n" +
                "stored chunks and cannot be undone). Its buttons are disabled\n" +
                "automatically. The notification server (NTF) reports statistics\n" +
                "and information only.")
        l.TextStyle = fyne.TextStyle{Italic: true}
        l.Wrapping = fyne.TextWrapWord
        return l
}

// refresh reloads stats, clients and server info for the selected server.
func (v *adminView) refresh() {
        if v.busy {
                return
        }
        go v.runAsync(v.fetchAll)
}

// fetchAll gathers every panel output for the selected kind.
func (v *adminView) fetchAll() (string, error) {
        k := v.selected
        rec := v.c.eng.State.Server(k)
        // the moderation card's title / copy / labels always follow the
        // selected kind (also for the not-installed / not-running paths)
        fyne.Do(func() { v.setBanCardUI(k) })
        if !rec.Installed {
                fyne.Do(v.resetPanels)
                fyne.Do(func() { v.setGateUI(false, false) })             // hides the card
                fyne.Do(func() { v.setReportsUI(k, "", "", nil, false) }) // hides the card
                return fmt.Sprintf("%s is not installed yet - run the setup first.", core.Def(k).Name), nil
        }
        // the [AUTH] registration gate applies to the INI, so it is live
        // even while the server is stopped (applies at the next start)
        if core.NewLinksSupported(k) {
                blocked, err := v.c.eng.NewLinksBlocked(k)
                fyne.Do(func() { v.setGateUI(blocked && err == nil, err == nil) })
        } else {
                fyne.Do(func() { v.setGateUI(false, false) })
        }
        // the complaints contacts are likewise INI-backed and live even
        // while the server is stopped (they publish at the next start)
        if core.ComplaintsSupported(k) {
                simplex, email, cerr := v.c.eng.Complaints(k)
                fyne.Do(func() { v.setReportsUI(k, simplex, email, cerr, cerr == nil) })
        } else {
                fyne.Do(func() { v.setReportsUI(k, "", "", nil, false) }) // hides the card
        }
        if !v.c.eng.Backend.ServerRunning(k) {
                fyne.Do(v.resetPanels)
                return fmt.Sprintf("%s is not running - start it from the Dashboard first.", core.Def(k).Name), nil
        }
        var sb strings.Builder

        // stats
        if core.ControlSupported(k, "stats") {
                st, err := core.FetchStats(k, rec)
                if err != nil {
                        sb.WriteString("stats: " + err.Error() + "\n")
                } else {
                        // keys as printed by the servers (v6.5.0): qCount, qCreated,
                        // qSecured, qDeletedAll, "daily active queues", msgCount,
                        // msgSent, msgRecv, notifiers, Clients, fromTime ...
                        order := []string{"fromTime", "qCount", "qCreated", "qSecured", "qDeletedAll", "daily active queues", "msgCount", "msgSent", "msgRecv", "notifiers", "Clients"}
                        labels := map[string]string{
                                "qCount": "message queues (chats)", "qCreated": "queues created today",
                                "qSecured": "queues secured", "qDeletedAll": "queues deleted",
                                "daily active queues": "queues active today",
                                "msgCount":            "messages stored", "msgSent": "messages sent today",
                                "msgRecv": "messages received today", "notifiers": "notifiers used",
                                "Clients": "connected clients",
                        }
                        var rows []string
                        for _, key := range order {
                                val := st.Get(key)
                                if val == "" || val == "0" {
                                        continue
                                }
                                if key == "fromTime" {
                                        if ts := epochFmt(val); ts != "" {
                                                rows = append(rows, "server up since "+ts)
                                        }
                                        continue
                                }
                                if label, ok := labels[key]; ok {
                                        rows = append(rows, fmt.Sprintf("%s: %s", label, val))
                                }
                        }
                        if len(rows) == 0 {
                                rows = append(rows, "no activity recorded yet")
                        }
                        stats := strings.Join(rows, "\n")
                        fyne.Do(func() { v.statLbl.SetText(stats) })
                }
        } else {
                fyne.Do(func() { v.statLbl.SetText("(this server does not report live statistics)") })
        }

        // clients
        if core.ControlSupported(k, "clients") {
                raw, err := core.FetchRaw(k, rec, "clients", "")
                if err == nil {
                        rows := core.ParseClients(raw)
                        fyne.Do(func() { v.renderClients(rows) })
                } else {
                        fyne.Do(func() { v.renderClients(nil) })
                }
        } else {
                fyne.Do(func() {
                        v.clientsHead.SetText("Client list")
                        v.clientsCnt.SetText("(this server does not report a client list)")
                        v.clientsBox.Objects = nil
                        v.clientsBox.Refresh()
                })
        }

        // queues / files (moderation targets) from the store log
        if core.StoreLogName(k) != "" {
                queues, qerr := core.ListQueues(v.c.eng, k)
                fyne.Do(func() {
                        if qerr != nil {
                                v.lastQueues = nil
                                v.queuesHead.SetText(core.Def(k).QueuesTitle())
                                v.queuesCnt.SetText("(the list appears once " + core.Def(k).Name + " is running)")
                                v.queuesBox.Objects = nil
                                v.queuesBox.Refresh()
                                return
                        }
                        v.renderQueues(queues)
                })
        } else {
                fyne.Do(func() {
                        v.lastQueues = nil
                        v.queuesHead.Hide()
                        v.queuesCnt.Hide()
                        v.queuesBox.Objects = nil
                })
        }

        // server-info
        if core.ControlSupported(k, "server-info") {
                raw, err := core.FetchRaw(k, rec, "server-info", "")
                if err == nil {
                        fyne.Do(func() { v.infoEntry.SetText(strings.TrimSpace(raw)) })
                } else {
                        fyne.Do(func() { v.infoEntry.SetText("server-info: " + err.Error()) })
                }
        } else {
                fyne.Do(func() { v.infoEntry.SetText("(this server does not report server information)") })
        }

        // command availability
        canBlock := core.ControlSupported(k, "block")
        canUnblock := core.ControlSupported(k, "unblock")
        canStatus := core.ControlSupported(k, "status")
        canDelete := core.ControlSupported(k, "delete")
        fyne.Do(func() {
                setEnabled(v.banBtn, canBlock)
                setEnabled(v.unbanBtn, canUnblock)
                setEnabled(v.banStatusBtn, canStatus)
                setEnabled(v.banDeleteBtn, canDelete)
                v.renderBans()
        })

        return sb.String(), nil
}

// resetPanels clears every admin panel (used when the selected server is
// not installed or not running, so no stale data from a previously
// selected server lingers on screen). Must run on the UI thread.
func (v *adminView) resetPanels() {
        v.setBanCardUI(v.selected)
        v.statLbl.SetText("")
        v.clientsHead.SetText("Connected clients")
        v.clientsCnt.SetText("")
        v.clientsBox.Objects = nil
        v.clientsBox.Refresh()
        v.queuesHead.SetText(core.Def(v.selected).QueuesTitle())
        v.queuesCnt.SetText("")
        v.queuesBox.Objects = nil
        v.queuesBox.Refresh()
        v.infoEntry.SetText("")
        v.lastQueues = nil
        v.bansCnt.SetText("")
        v.bansBox.Objects = nil
        v.bansBox.Refresh()
        v.banBtn.Disable()
        v.unbanBtn.Disable()
        v.banStatusBtn.Disable()
        v.banDeleteBtn.Disable()
}

// setEnabled enables/disables a button on the main thread-safe helper.
func setEnabled(b *widget.Button, ok bool) {
        if ok {
                b.Enable()
        } else {
                b.Disable()
        }
}

// renderQueues draws the moderation target list (chat queues for SMP,
// files for XFTP) with an identifying local date AND time per row, a
// marker for live connection links (reusable contact / channel / group /
// relay addresses - v1.6.31: only genuine contact-mode links are marked,
// so ordinary users' queues never show as links), and Ban / Unban /
// Delete buttons.
func (v *adminView) renderQueues(rows []core.QueueInfo) {
        k := v.selected
        v.lastQueues = rows
        v.queuesHead.SetText(core.Def(k).QueuesTitle())
        v.queuesHead.Show()
        empty := "Nothing here yet - a queue appears when somebody starts a chat via your server."
        idHead := "queue ID"
        whenHead := "first seen"
        if k == core.KindXFTP {
                empty = "Nothing here yet - a file appears when somebody shares a file in a chat via your server."
                idHead = "file ID"
                // the date sits in the per-day group headers (the server
                // records creation hours); rows carry the time of day only
                whenHead = "time"
        }
        if len(rows) == 0 {
                v.queuesCnt.SetText(empty)
                v.queuesBox.Objects = nil
                v.queuesBox.Refresh()
                return
        }
        total := fmt.Sprintf("%d total", len(rows))
        if k == core.KindXFTP {
                // one row per file recipient (the ID the moderation commands accept)
                total += " - listed per file recipient"
        }
        links := 0
        for _, q := range rows {
                if q.LinkLive {
                        links++
                }
        }
        if links > 0 {
                // queues marked "link" carry a live connection link - a
                // one-time invitation code or a reusable contact / group
                // address - banning them kills it
                total += fmt.Sprintf(" - %d live connection links", links)
        }
        if k == core.KindSMP {
                // the SMP server records no queue creation times; the column
                // shows when sxops first listed each queue
                total += " - times are when sxops first saw the queue"
        }
        if k == core.KindXFTP {
                // files: the time column shows the stored chunk file's own
                // upload time (second precision, v1.6.27) - the journal's
                // hour-rounded creation time is the fallback for rows
                // whose chunks are already gone (blocked files); the size
                // identifies which file a report refers to; rows are
                // grouped by day (v1.6.21)
                total += " - upload times are the stored files' own, to the second (blocked rows: the journal's hour), grouped by day"
        }
        v.queuesCnt.SetText(total)
        headerEls := []fyne.CanvasObject{
                cell(idHead, 260),
                cell(whenHead, 130),
        }
        if k == core.KindXFTP {
                headerEls = append(headerEls, cell("size", 80))
        }
        headerEls = append(headerEls,
                cell("state", 70),
                cell("", 90),
        )
        header := container.NewHBox(headerEls...)
        var objs []fyne.CanvasObject
        objs = append(objs, header, widget.NewSeparator())
        show := rows
        if k == core.KindXFTP {
                // group by day: order the file rows by their identifying
                // timestamp first (oldest first), so the day headers
                // partition the list cleanly; rows without any timestamp
                // keep their first-seen order at the top
                show = append([]core.QueueInfo(nil), rows...)
                sort.SliceStable(show, func(i, j int) bool {
                        return queueWhenEpoch(k, show[i]) < queueWhenEpoch(k, show[j])
                })
        }
        if len(show) > 100 {
                show = show[len(show)-100:] // newest at the end
        }
        lastDay := ""
        for _, q := range show {
                id := q.ID
                if len(id) > 28 {
                        id = id[:28] + "…"
                }
                when := orDash(queueWhen(k, q))
                if k == core.KindXFTP {
                        day, hm := queueWhenParts(k, q)
                        if day != "" {
                                // a bold day header starts each date group
                                // (the rows below it carry the time only)
                                if day != lastDay {
                                        lastDay = day
                                        h := widget.NewLabel(day)
                                        h.TextStyle = fyne.TextStyle{Bold: true}
                                        objs = append(objs, h)
                                }
                                when = hm
                        } else {
                                when = "–"
                        }
                }
                state := "active"
                if q.Blocked {
                        state = "banned"
                        if k == core.KindXFTP {
                                // the upstream journal records FBLK for blocked
                                // files - the chunks are already deleted
                                state = "blocked"
                        }
                } else if q.LinkLive {
                        // live connection link (a reusable contact /
                        // channel / group / relay address - v1.6.31: the
                        // store log only marks contact-mode queues as
                        // links, so this is a genuine address): the row's
                        // Ban button opens the link dialog (v1.6.33: block
                        // the link or delete the queue behind it) -
                        // blocking this queue by its own ID or by the
                        // link ID is the same official block, and it kills
                        // the address (LGET fails once the queue is
                        // blocked)
                        state = "link"
                }
                var btn *widget.Button
                switch {
                case q.Blocked && k == core.KindXFTP:
                        // file blocks cannot be undone - the chunks are
                        // already gone; only deleting the record remains
                        btn = widget.NewButton("Delete", func() {
                                v.banIDEntry.SetText(q.ID)
                                v.confirmDelete()
                        })
                        btn.Importance = widget.DangerImportance
                case q.Blocked:
                        btn = widget.NewButton("Unban", func() {
                                v.banIDEntry.SetText(q.ID)
                                v.doUnban()
                        })
                default:
                        label := "Ban"
                        if k == core.KindXFTP {
                                label = "Block"
                        }
                        btn = widget.NewButton(label, func() {
                                v.banIDEntry.SetText(q.ID)
                                v.banDialog(q.ID)
                        })
                        btn.Importance = widget.DangerImportance
                }
                rowEls := []fyne.CanvasObject{
                        monoCell(id, 260),
                        cell(when, 130),
                }
                if k == core.KindXFTP {
                        rowEls = append(rowEls, cell(orDash(humanSize(q.FileSize)), 80))
                }
                rowEls = append(rowEls,
                        cell(state, 70),
                        container.NewGridWrap(fyne.NewSize(90, btn.MinSize().Height), btn),
                )
                objs = append(objs, container.NewHBox(rowEls...))
        }
        v.queuesBox.Objects = objs
        v.queuesBox.Refresh()
}

// queueWhenEpoch returns the identifying timestamp of a queue/file
// row as epoch seconds (0 when unknown) - the XFTP day-grouping sort
// key. Files prefer the stored chunk's own upload time (v1.6.27) and
// fall back to the journal's hour-rounded creation time.
func queueWhenEpoch(k core.Kind, q core.QueueInfo) int64 {
        if k == core.KindXFTP {
                for _, ts := range []string{q.UploadedAt, q.CreatedAt} {
                        if n, err := strconv.ParseInt(strings.TrimSpace(ts), 10, 64); err == nil && n > 0 {
                                return n
                        }
                }
        }
        if t, err := time.Parse(time.RFC3339, q.SeenAt); err == nil {
                return t.Unix()
        }
        return 0
}

// humanSize renders a byte count (decimal string, as recorded by the
// XFTP server's FNEW log) as "12 B", "3.4 KB", "1.2 MB", "470.0 GB"
// ("" when absent or invalid) - the size identifies which file a
// report refers to together with the creation time.
func humanSize(b string) string {
        n, err := strconv.ParseInt(strings.TrimSpace(b), 10, 64)
        if err != nil || n < 0 {
                return ""
        }
        switch {
        case n < 1024:
                return fmt.Sprintf("%d B", n)
        case n < 1024*1024:
                return fmt.Sprintf("%.1f KB", float64(n)/1024)
        case n < 1024*1024*1024:
                return fmt.Sprintf("%.1f MB", float64(n)/(1024*1024))
        default:
                return fmt.Sprintf("%.1f GB", float64(n)/(1024*1024*1024))
        }
}

// The v1.6.22 by-hash helpers (the digest column and the by-hash ban)
// were removed in v1.6.27 together with every SHA-256 reference: the
// file list shows ID, upload time, size and state, and the ban field
// takes file IDs only.

// queueWhen renders the identifying timestamp of a queue/file row as a
// local date AND time: the stored file's own upload time for files
// (second precision - v1.6.27; the journal's hour-rounded creation time
// is the fallback) or the sxops first-seen time for chat queues (the
// SMP server records no queue times at all). "" when neither is known.
func queueWhen(k core.Kind, q core.QueueInfo) string {
        d, hm := queueWhenParts(k, q)
        if d == "" {
                return ""
        }
        return d + " " + hm
}

// queueWhenParts splits the identifying timestamp into its date and
// time-of-day parts (both local). The XFTP list groups rows by the
// date part and shows only the time per row; the SMP list shows the
// full date + time per row. Files prefer the stored chunk's own mtime
// (v1.6.27, shown with seconds); rows whose chunks are gone fall back
// to the journal's hour-rounded creation time, shown as HH:MM.
func queueWhenParts(k core.Kind, q core.QueueInfo) (date, hm string) {
        if k == core.KindXFTP {
                if n, err := strconv.ParseInt(strings.TrimSpace(q.UploadedAt), 10, 64); err == nil && n > 0 {
                        t := time.Unix(n, 0).Local()
                        return t.Format("2006-01-02"), t.Format("15:04:05")
                }
                if n, err := strconv.ParseInt(strings.TrimSpace(q.CreatedAt), 10, 64); err == nil && n > 0 {
                        t := time.Unix(n, 0).Local()
                        return t.Format("2006-01-02"), t.Format("15:04")
                }
        }
        if t, err := time.Parse(time.RFC3339, q.SeenAt); err == nil {
                t = t.Local()
                return t.Format("2006-01-02"), t.Format("15:04")
        }
        return "", ""
}

// renderClients draws the clients table with REAL dates and times:
// the servers report raw epoch seconds, which are converted to the
// local time zone here (age becomes a human duration).
func (v *adminView) renderClients(rows []core.ClientsRow) {
        v.clientsHead.SetText("Connected clients")
        if len(rows) == 0 {
                v.clientsCnt.SetText("No clients are connected right now. This is normal - clients connect only while sending or receiving messages.")
                v.clientsBox.Objects = nil
                v.clientsBox.Refresh()
                return
        }
        v.clientsCnt.SetText(fmt.Sprintf("%d connected - times are local, loaded at %s",
                len(rows), time.Now().Format("2006-01-02 15:04:05")))
        header := container.NewHBox(
                cell("client", 190),
                cell("up", 50),
                cell("connected since", 150),
                cell("last active", 150),
                cell("age", 90),
        )
        var objs []fyne.CanvasObject
        objs = append(objs, header, widget.NewSeparator())
        show := rows
        if len(show) > 50 {
                show = show[:50]
        }
        for _, r := range show {
                cid := r.ClientID
                if len(cid) > 20 {
                        cid = cid[:20] + "…"
                }
                up := "no"
                if strings.EqualFold(strings.TrimSpace(r.Connected), "true") {
                        up = "yes"
                }
                // last activity = the more recent of send/receive (0 = never)
                last := maxEpoch(r.SndActiveAt, r.RcvActiveAt)
                objs = append(objs, container.NewHBox(
                        monoCell(cid, 190),
                        cell(up, 50),
                        cell(orDash(epochFmt(r.CreatedAt)), 150),
                        cell(orDash(epochFmt(last)), 150),
                        cell(orDash(humanDur(r.Age)), 90),
                ))
        }
        v.clientsBox.Objects = objs
        v.clientsBox.Refresh()
}

// ---- client moderation (all official simplexmq methods) ----

// banCardCopy is the moderation card description per kind. The XFTP
// copy follows the upstream file-block semantics (simplexmq
// FileTransfer/Server.hs and FileTransfer/Server/Control.hs: block and
// delete are the ONLY moderation commands the file server has - there
// is no uploader identity and no way to block future uploads); the
// report channels are the in-app moderator reports (E2E chat messages
// quoting the content - no links) and the complaints address published
// on the info web page (the Abuse reports card).
func banCardCopy(k core.Kind) string {
        if k == core.KindXFTP {
                return "Blocking a file is the official SimpleX content moderation for file servers: the stored chunks are removed from the server right away and the file is marked blocked with your reason, so every further access returns the official BLOCKED error - the 6.3+ apps display it. It cannot be undone.\nDeleting the file is the other official method: the record and the chunks are removed, so clients get a plain no-file error instead of your reason. These two are everything the file server offers - there are no user accounts and no uploader identity to ban, and no way to block future uploads.\nHow files get reported: users report them to the group moderators first (end-to-end encrypted chats that never touch this server); the report quotes the file's name, size and arrival time - so find the matching rows in the file list above by upload time and size, and paste the file ID from there. File-server blocks are not limited by the chat server's ~20 per day quota."
        }
        return "Banning is the official SimpleX moderation method. What you paste decides which official options the dialog lists, each with its own explanation: banning a client (a queue or link ID from the lists) lists the restrictions on that user - block the queue (their messages through that chat queue stop in both directions, undoable with Unban), block link creation (the official ban notice that makes the banned user's app refuse to create new links via your server for the chosen time) and/or delete the queue permanently.\nBanning a pasted link - a one-time invitation, contact address or group / channel / relay link in either of its forms (the https:// link or the compact simplex:/ one the apps share) - or a queue the list marks \"link\" opens the link dialog (v1.6.33): the link block, with a reason and no expiry - that link stops working for everybody, new joins get the official BLOCKED error with your reason, and it lasts until you Unban - and/or the permanent delete of the queue behind the link, in one go. Bare smp:// or xftp:// addresses are not ban inputs - smp:// is a server address (what the apps put in server settings); paste the link itself instead.\nAbuse complaints arrive as ordinary chats to your complaints address - they carry no smp:// addresses, only whatever link the reporter chose to include; when there is no link, find the queue by the reported time in the lists and the store log below, and paste its ID here. Banning a queue with a live connection link (marked \"link\" in the list) kills that address. The chat server records why you banned (spam or content) and allows about 20 bans per day.\nEvery ban is recorded by the server itself in its store log - the Ban list below is read from it, so it also shows bans placed outside sxops."
}

// serverInfoCardSubtitle is the "Server information" card's copy
// (v1.6.38). v1.6.37 reworded the sentence to "Version, uptime, etc.
// is reported by the server."; the operator asked to revert that and
// keep the original sentence with exactly one word changed: "as" ->
// "is". It stays a plain function so the wording is pinnable in a
// test, like retryStageText in the Tor panel.
func serverInfoCardSubtitle() string {
        return "Version, uptime and configuration is reported by the server."
}

// noticeExpiryOptions are the lifetimes offered for the link-creation
// block (v1.6.21: the expiry selector lives on that option) - the TTL
// of the official ban notice, i.e. how long the banned client's app
// refuses to create new links via this server. "1 day" is deliberately
// absent (v1.6.14) and there is no "queue only" - the unticked check
// mark is it (v1.6.15).
var noticeExpiryOptions = []string{"12 hours", "2 days", "7 days", "30 days", "1 year", "no expiry"}

// noticeExpiryTTLOf maps an expiry label to the notice TTL in seconds
// (0 = no expiry, ClientNotice {"ttl":null} - the app refuses new
// links until the queue is unblocked). ok is false for labels outside
// the offered list.
func noticeExpiryTTLOf(label string) (int64, bool) {
        switch label {
        case "12 hours":
                return 43200, true
        case "2 days":
                return 172800, true
        case "7 days":
                return 604800, true
        case "30 days":
                return 2592000, true
        case "1 year":
                return 31536000, true
        case "no expiry":
                return 0, true
        }
        return 0, false
}

// noticeExpiryUntil renders a notice TTL as the phrase the dialog
// summaries use: "for 2 days", or "until you Unban" when there is no
// expiry.
func noticeExpiryUntil(ttl int64) string {
        if ttl <= 0 {
                return "until you Unban"
        }
        for _, o := range noticeExpiryOptions {
                if t, ok := noticeExpiryTTLOf(o); ok && t == ttl {
                        return "for " + o
                }
        }
        return fmt.Sprintf("for %d seconds", ttl)
}

// epochFmt renders an epoch-seconds timestamp reported by the servers
// as a local date and time ("" when absent/invalid).
func epochFmt(sec string) string {
        n, err := strconv.ParseInt(strings.TrimSpace(sec), 10, 64)
        if err != nil || n <= 0 {
                return ""
        }
        return time.Unix(n, 0).Local().Format("2006-01-02 15:04:05")
}

// humanDur renders a duration in seconds as e.g. "45s", "3m 12s",
// "2h 5m", "4d 3h" ("" when invalid).
func humanDur(sec string) string {
        n, err := strconv.ParseInt(strings.TrimSpace(sec), 10, 64)
        if err != nil || n < 0 {
                return ""
        }
        switch {
        case n < 60:
                return fmt.Sprintf("%ds", n)
        case n < 3600:
                return fmt.Sprintf("%dm %ds", n/60, n%60)
        case n < 86400:
                return fmt.Sprintf("%dh %dm", n/3600, (n%3600)/60)
        default:
                return fmt.Sprintf("%dd %dh", n/86400, (n%86400)/3600)
        }
}

// maxEpoch returns the larger of two epoch-seconds values ("" when both
// are absent).
func maxEpoch(a, b string) string {
        pa, ea := strconv.ParseInt(strings.TrimSpace(a), 10, 64)
        pb, eb := strconv.ParseInt(strings.TrimSpace(b), 10, 64)
        if ea != nil || pa <= 0 {
                return b
        }
        if eb != nil || pb <= 0 {
                return a
        }
        if pb > pa {
                return b
        }
        return a
}

func cell(text string, w int) fyne.CanvasObject {
        l := widget.NewLabel(orDash(text))
        l.Wrapping = fyne.TextWrapOff
        return container.NewGridWrap(fyne.NewSize(float32(w), l.MinSize().Height), l)
}

func monoCell(text string, w int) fyne.CanvasObject {
        l := widget.NewLabel(orDash(text))
        l.TextStyle = fyne.TextStyle{Monospace: true}
        l.Wrapping = fyne.TextWrapOff
        return container.NewGridWrap(fyne.NewSize(float32(w), l.MinSize().Height), l)
}

func orDash(s string) string {
        if strings.TrimSpace(s) == "" || s == "null" {
                return "–"
        }
        return s
}

// ---- client moderation actions ----

// banTarget returns the moderation target from the entry field: the
// pasted text may be a bare queue / link / file ID or one of the links
// the apps share - a contact address, group / channel / relay
// short link (https://server/g#... or the compact simplex:/g#...?h=..),
// a one-time invitation, or a page-wrapped full contact link (the link
// ID is derived from the link key exactly like the official clients do
// it). All of them resolve to an ID the servers accept for block /
// unblock / status / delete.
func (v *adminView) banTarget() string {
        return core.ParseTargetID(v.banIDEntry.Text)
}

// banTargetShape classifies WHAT is getting banned from the entry
// text (v1.6.21), so the ban dialog can offer only the options that
// fit the target. A pasted connection link - a one-time invitation or
// a reusable contact / group / channel / relay link, in either of its
// forms (the https spelling or the compact simplex:/ one) - is a LINK
// ban. A bare token that matches a live link ID known from the queue
// lists is a link ban too (v1.6.31: only genuine contact-mode links
// are in that map, so ordinary users' queue IDs never match), and so
// is the QUEUE ID of a row the list itself marks "link" (v1.6.32:
// that row's Ban button pastes the queue ID - blocking it by the
// queue ID or by the link ID is the same official block, and both
// kill the address). Everything else - a queue ID, an unknown bare
// ID - is a USER ban. The file server ignores the shape (its dialog
// is file-shaped: files carry no links).
func (v *adminView) banTargetShape(id string) core.TargetShape {
        _, shape := core.ParseTargetIDShape(v.banIDEntry.Text)
        if shape == core.ShapeBareID && id != "" {
                for _, q := range v.lastQueues {
                        if q.ID == id && q.LinkLive && !q.Blocked {
                                return core.ShapeLink
                        }
                        if q.LinkID != "" && q.LinkID == id && q.ID != id {
                                return core.ShapeLink
                        }
                }
        }
        return shape
}

// doBan bans the client currently entered in the ban form. When
// nothing parsed, the forms that are deliberately not accepted (bare
// server addresses, the compact in-app link form) get a targeted
// explanation instead of a bare "empty ID" error.
func (v *adminView) doBan() {
        id := v.banTarget()
        if id == "" {
                showError(v.c.win, fmt.Errorf("%s", core.TargetInputHint(v.banIDEntry.Text)))
                return
        }
        v.banDialog(id)
}

// banChoices is the set of restrictions the operator ticked in the ban
// dialog - what happens to THE TARGET getting moderated, chosen one by
// one (every option independently tickable), and only from the
// official moderation commands of the selected server that FIT the
// target (v1.6.21: the dialog adapts to what is getting banned):
//   - block: strip the ability to send and receive through the queue
//     (SMP user ban: the official queue block) / block the file (XFTP:
//     the official content moderation - the reason is what the BLOCKED
//     error displays).
//   - linkCreation: block link creation (SMP user ban ONLY) - the
//     official ban notice rides the block, and the banned client's app
//     refuses to create new links via this server for noticeTTL
//     (0 = no expiry). v1.6.21: the expiry select lives on this option
//     and the reason does not - the reason is only choosable where
//     somebody sees it, on the link ban.
//   - link: block the connection to that link (SMP link ban ONLY -
//     v1.6.33: the link dialog's block option, reason and no expiry) -
//     the link stops working for everybody and new joins get the
//     official BLOCKED error with the chosen reason.
//   - del: delete the queue (and its undelivered messages) / the file
//     permanently - on a link ban (v1.6.33) it is the queue behind
//     the link that goes.
type banChoices struct {
        block        bool
        link         bool
        linkCreation bool
        del          bool
        reason       string
        noticeTTL    int64 // notice lifetime in seconds; 0 = no expiry
}

// valid reports whether the combination does anything at all.
func (c banChoices) valid() bool {
        return c.block || c.link || c.linkCreation || c.del
}

// withNotice reports whether the official ban notice is sent: it rides
// the one official block command when the link-creation option is
// ticked (a user-ban restriction - link bans send no notice).
func (c banChoices) withNotice() bool { return c.linkCreation }

// reasonVisible reports whether the reason select shows in the dialog:
// it belongs to the option whose command records it FOR SOMEBODY TO
// SEE - the XFTP file block (the reason is what the file is marked
// with) and the SMP link block (new joins get the BLOCKED error with
// the reason). The SMP user ban has no reason row at all (v1.6.21) -
// there the server records its default and only the ban list shows it.
func reasonVisible(k core.Kind, shape core.TargetShape, c banChoices) bool {
        if k == core.KindXFTP {
                return c.block
        }
        return shape == core.ShapeLink && c.link
}

// banReasonToSend is the reason actually recorded by the server: the
// chosen reason when the option that owns it is ticked, the official
// default (spam) otherwise - a value chosen for an option that is not
// happening is never sent.
func banReasonToSend(k core.Kind, shape core.TargetShape, c banChoices) string {
        if reasonVisible(k, shape, c) {
                return c.reason
        }
        return core.BanReasonSpam
}

// banRestrictionSummary renders the numbered list of restrictions the
// chosen combination places on the target (the dialog's live summary),
// following the target's shape (v1.6.21): a link ban never lists the
// queue block (the dialog has no such option - the link block is
// delivered through the official queue block); a user ban lists the
// link-creation block with its expiry phrase. v1.6.33: the link ban
// lists the block and, when ticked in the link dialog, the delete of
// the queue behind the link. The XFTP
// wording follows the upstream file-block semantics
// (FileTransfer/Server.hs blockServerFile): the block deletes the
// stored chunks and records the reason; there is no unblock.
func banRestrictionSummary(c banChoices, k core.Kind, shape core.TargetShape) string {
        var sb strings.Builder
        n := 1
        if k == core.KindXFTP {
                if c.block {
                        sb.WriteString("1. Block the file - the stored chunks are removed now; every\n   further access returns the official BLOCKED error with your\n   reason.\n")
                        n++
                }
                // there is no notice restriction on the file server: no
                // uploader identity, no upload blocking
        } else if shape == core.ShapeLink {
                // v1.6.33: the link ban's own restriction is the block; the
                // delete (a link-dialog option since v1.6.33) renders through
                // the shared delete line below
                if c.link {
                        fmt.Fprintf(&sb, "%d. Block the link - it stops working for everybody: new joins\n   get the official BLOCKED error with your reason, until you\n   Unban.\n", n)
                        n++
                }
        } else {
                if c.block {
                        fmt.Fprintf(&sb, "%d. Block the queue - their messages through it stop in both\n   directions (undo any time with Unban).\n", n)
                        n++
                }
                if c.linkCreation {
                        fmt.Fprintf(&sb, "%d. Block link creation - their app refuses to create new links\n   via your server %s, riding the queue block (undo with Unban).\n", n, noticeExpiryUntil(c.noticeTTL))
                        n++
                }
        }
        if c.del {
                if k == core.KindXFTP {
                        fmt.Fprintf(&sb, "%d. Delete the file outright - clients get a plain\n   \"no file\" error, without your reason. Cannot be undone.\n", n)
                } else if shape == core.ShapeLink {
                        fmt.Fprintf(&sb, "%d. Delete the queue behind the link and its undelivered\n   messages permanently (cannot be undone).\n", n)
                } else {
                        fmt.Fprintf(&sb, "%d. Delete the %s permanently (cannot be undone).\n", n, "queue and its undelivered messages")
                }
        }
        return sb.String()
}

// banDialog is the ADAPTIVE ban flow (v1.6.21; v1.6.28 slimmed the
// copy and capped the dialog to the window; v1.6.32 split the link
// ban out into its own dialog - two options since v1.6.33): a single
// Ban/Block button
// opens a dialog that ADAPTS TO WHAT IS GETTING BANNED - it lists ONLY
// the official commands that fit that target, each option with a short
// explanation right under it:
//   - SMP, banning a LINK (a pasted one-time invitation, contact /
//     group / channel / relay link, or a queue the list marks "link"):
//     linkBanDialog below - "Block the link", with the reason new
//     joins will see in the official BLOCKED error and NO expiry (a
//     block has none - the only TTL in the protocol is the user-ban
//     notice, which does not apply to a link), plus the delete of the
//     queue behind the link (v1.6.33). Nothing is ticked by default,
//     like the user dialog.
//   - SMP, banning a USER (a queue ID): "Block the queue" (the
//     official queue block - their messages stop in both directions,
//     undoable with Unban), "Block link creation" (the official ban
//     notice - the banned user's app refuses to create new links via
//     this server for the chosen time; the expiry selector lives on
//     this option, there is no reason row there) and "Delete the queue
//     permanently". NOTHING is ticked by default - the dialog opens
//     with the Ban button disabled and a "tick at least one
//     restriction" note, and only the restrictions the operator
//     consciously ticks are executed.
//   - XFTP (a file): "Block the file (official content moderation)"
//     (the reason is what the BLOCKED error displays) and "Delete the
//     file instead" - the two official file methods, nothing else
//     (there is no uploader identity to restrict and no notice option
//     on the file server - nothing to notify).
//
// On SMP there is no notice-only control command, so the notice
// travels inside the ONE official block command - ticking only "Block
// link creation" still sends that command, and the live summary says
// so. The server-wide new-registrations gate is deliberately NOT here:
// it restricts everybody, not this target - it has its own card below
// the ban list.
func (v *adminView) banDialog(clientID string) {
        if err := coreValidClientID(clientID); err != nil {
                showError(v.c.win, err)
                return
        }
        k := v.selected
        shape := v.banTargetShape(clientID)
        if k != core.KindXFTP && shape == core.ShapeLink {
                // v1.6.32: a connection link has its own dialog below (the
                // block and, since v1.6.33, the delete of the queue behind
                // it) - the user restrictions do not apply to it
                v.linkBanDialog(clientID)
                return
        }
        isXFTP := k == core.KindXFTP

        // nothing ticked by default (v1.6.16): the operator consciously
        // chooses what happens; Ban stays disabled until at least one
        // restriction is ticked
        ch := banChoices{reason: core.BanReasonSpam, noticeTTL: 172800}

        // --- restriction 1: the queue / file block ---
        blockChk := widget.NewCheck("Block the queue", nil)
        blockNote := widget.NewLabel("    Their messages through this queue stop in both directions -\n" +
                "    the official queue block. You can undo it any time with Unban.")
        if isXFTP {
                blockChk.SetText("Block the file (official content moderation)")
                // verified against simplexmq FileTransfer/Server.hs
                // blockServerFile: the block DELETES the stored chunks at
                // block time and marks the record EntityBlocked - there is
                // NO unblock on the file server
                blockNote.SetText("    The stored chunks are removed right away and the file is\n" +
                        "    marked blocked with your reason - every later access returns\n" +
                        "    the official BLOCKED error. This cannot be undone.")
        }
        blockNote.TextStyle = fyne.TextStyle{Italic: true}
        blockNote.Wrapping = fyne.TextWrapOff

        // the reason row: XFTP only - under the file block. SMP user
        // bans have no reason row (v1.6.21): "Block link creation" has
        // the expiry, not the reason - nobody but the ban list ever sees
        // the reason there, so the server records its default. (SMP LINK
        // bans carry their reason row inside linkBanDialog.)
        var reasonRow *fyne.Container
        if isXFTP {
                reasonSel := widget.NewSelect([]string{"Spam", "Inappropriate content"}, func(s string) {
                        if s == "Inappropriate content" {
                                ch.reason = core.BanReasonContent
                        } else {
                                ch.reason = core.BanReasonSpam
                        }
                })
                reasonSel.PlaceHolder = "Spam"
                reasonSel.SetSelected("Spam")
                reasonRow = container.NewBorder(nil, nil, widget.NewLabel("    Reason recorded by the server:"), nil, reasonSel)
        }

        // --- restriction 2 (SMP user ban only: block link creation
        // with its expiry select; the file server has only the two
        // official file methods, block + delete) ---
        var opt2Chk *widget.Check
        var opt2Note *widget.Label
        var expiryRow *fyne.Container
        if !isXFTP {
                opt2Chk = widget.NewCheck("Block link creation", nil)
                opt2Note = widget.NewLabel("    The official ban notice: their app refuses to create new\n" +
                        "    links via your server for the chosen time - it rides the\n" +
                        "    queue block, so messages through this queue stop too\n" +
                        "    (undo with Unban).")
                expirySel := widget.NewSelect(noticeExpiryOptions, func(s string) {
                        if ttl, ok := noticeExpiryTTLOf(s); ok {
                                ch.noticeTTL = ttl
                        }
                })
                expirySel.PlaceHolder = "2 days"
                expirySel.SetSelected("2 days")
                expiryRow = container.NewBorder(nil, nil, widget.NewLabel("    For how long:"), nil, expirySel)
        }
        if opt2Note != nil {
                opt2Note.TextStyle = fyne.TextStyle{Italic: true}
                opt2Note.Wrapping = fyne.TextWrapOff
        }

        // --- restriction 3: permanent deletion ---
        delLabel := "Delete the queue permanently"
        delText := "    The queue and any undelivered messages stored for it are\n" +
                "    gone for good - there is no undo."
        deleteChk := widget.NewCheck(delLabel, nil)
        deleteNote := widget.NewLabel(delText)
        if isXFTP {
                deleteChk.SetText("Delete the file instead (no reason shown)")
                deleteNote.SetText("    The chunks and the file record are removed - clients get a\n" +
                        "    plain \"no file\" error instead of your reason. No undo.")
        }
        deleteNote.TextStyle = fyne.TextStyle{Italic: true}
        deleteNote.Wrapping = fyne.TextWrapOff

        // live summary + dependent enablement, recomputed on every change
        summary := widget.NewLabel("")
        summary.TextStyle = fyne.TextStyle{Italic: true}
        summary.Wrapping = fyne.TextWrapWord

        var banBtn *widget.Button
        upd := func() {
                if reasonRow != nil {
                        if reasonVisible(k, shape, ch) {
                                reasonRow.Show()
                        } else {
                                reasonRow.Hide()
                        }
                }
                if expiryRow != nil {
                        if ch.linkCreation {
                                expiryRow.Show()
                        } else {
                                expiryRow.Hide()
                        }
                }
                if !ch.valid() {
                        summary.SetText("Nothing is chosen - tick at least one restriction.")
                } else {
                        summary.SetText("What happens to " + banTargetWord(k, shape) + ":\n" + banRestrictionSummary(ch, k, shape))
                }
                if banBtn != nil {
                        setEnabled(banBtn, ch.valid())
                }
        }
        blockChk.OnChanged = func(on bool) { ch.block = on; upd() }
        if opt2Chk != nil {
                opt2Chk.OnChanged = func(on bool) { ch.linkCreation = on; upd() }
        }
        deleteChk.OnChanged = func(on bool) { ch.del = on; upd() }

        // per-target layout: user bans carry the expiry select under
        // "Block link creation"; XFTP carries the reason select under the
        // file block (its only other method is the delete)
        //
        // v1.6.28: the two v1.6.27 meta labels that named and explained
        // the target kind are gone - their single 150-character lines
        // pushed the dialog's min width past laptop-size windows, and a
        // popup whose min size exceeds the canvas is clamped by Fyne to
        // the full window instead of being centered (the dialog then
        // hugs the top-left corner). The options already say what they
        // do; capToWindow below is the safety net for small windows.
        head := []fyne.CanvasObject{
                widget.NewLabel(banActionWord(k, shape) + " " + shortBanTarget(clientID) + " on " + core.Def(k).Name + "."),
                widget.NewLabel("Choose what happens to " + banTargetWord(k, shape) + ":"),
                blockChk,
                blockNote,
        }
        var mid []fyne.CanvasObject
        if isXFTP {
                mid = []fyne.CanvasObject{reasonRow}
        } else {
                mid = []fyne.CanvasObject{widget.NewSeparator(), opt2Chk, expiryRow, opt2Note}
        }
        body := container.NewVBox(append(append(head, mid...),
                widget.NewSeparator(),
                deleteChk,
                deleteNote,
                widget.NewSeparator(),
                summary,
        )...)

        var d *widget.PopUp
        banBtn = dangerButton(banActionWord(k, shape), func() {
                if !ch.valid() {
                        return
                }
                d.Hide()
                v.runBan(clientID, ch, shape)
        })
        upd()

        d = widget.NewModalPopUp(container.NewVBox(
                widget.NewLabel(banActionWord(k, shape)+" this "+banTargetWord(k, shape)+"?"),
                capToWindow(v.c.win, body),
                container.NewHBox(
                        widget.NewButton("Cancel", func() { d.Hide() }),
                        banBtn,
                ),
        ), v.c.win.Canvas())
        d.Show()
}

// linkBanDialog (v1.6.33) is the LINK ban flow. Verified against the
// simplexmq control protocol: "block <id> reason=<spam|content>" is
// the official command that fits a connection link (the server
// resolves the link ID to the queue behind it and blocks the queue -
// Server.hs getSenderQueue's SSenderLink fallback), every later
// command addressed to the link - LGET, what a new join sends -
// returns the official BLOCKED error with the reason, and a block
// carries NO expiry (the only TTL anywhere in the protocol is the
// ban NOTICE of a user ban, which has no place here). v1.6.32 opened
// as the single fixed option; v1.6.33 adds the operator-requested
// Delete queue option - deleting the queue behind the link outright,
// the same official delete the user dialog and the card's own Delete
// button use. So the dialog is a two-option choice in the user
// dialog's shape: nothing ticked by default (v1.6.16 - the confirm
// button stays disabled until something is), the reason select
// riding the block option, still NO expiry field anywhere, and the
// confirm verb following what is ticked - Block when the block is on
// (alone or with the delete - it runs first), Delete when only the
// delete is.
func (v *adminView) linkBanDialog(clientID string) {
        k := v.selected
        shape := core.ShapeLink
        ch := banChoices{reason: core.BanReasonSpam}

        // --- option 1: the link block, with the reason new joins see ---
        blockChk := widget.NewCheck("Block the link", nil)
        blockNote := widget.NewLabel("    The link stops working for EVERYBODY: new joins get the\n" +
                "    official BLOCKED error with your reason. There is no\n" +
                "    expiry - it lasts until you Unban. It is delivered through\n" +
                "    the official queue block, so the queue's own traffic\n" +
                "    stops too.")
        blockNote.TextStyle = fyne.TextStyle{Italic: true}
        blockNote.Wrapping = fyne.TextWrapOff

        reasonSel := widget.NewSelect([]string{"Spam", "Inappropriate content"}, func(s string) {
                if s == "Inappropriate content" {
                        ch.reason = core.BanReasonContent
                } else {
                        ch.reason = core.BanReasonSpam
                }
        })
        reasonSel.PlaceHolder = "Spam"
        reasonSel.SetSelected("Spam")
        reasonRow := container.NewBorder(nil, nil, widget.NewLabel("    Reason recorded by the server:"), nil, reasonSel)

        // --- option 2 (v1.6.33): delete the queue behind the link ---
        deleteChk := widget.NewCheck("Delete the queue permanently", nil)
        deleteNote := widget.NewLabel("    The queue behind the link and any undelivered messages\n" +
                "    stored for it are gone for good - there is no undo.")
        deleteNote.TextStyle = fyne.TextStyle{Italic: true}
        deleteNote.Wrapping = fyne.TextWrapOff

        // live summary + dependent enablement, recomputed on every change
        // (the same upd pattern as the user dialog)
        summary := widget.NewLabel("")
        summary.TextStyle = fyne.TextStyle{Italic: true}
        summary.Wrapping = fyne.TextWrapWord

        var banBtn *widget.Button
        upd := func() {
                if reasonVisible(k, shape, ch) {
                        reasonRow.Show()
                } else {
                        reasonRow.Hide()
                }
                if !ch.valid() {
                        summary.SetText("Nothing is chosen - tick at least one option.")
                } else {
                        summary.SetText("What happens to " + banTargetWord(k, shape) + ":\n" + banRestrictionSummary(ch, k, shape))
                }
                if banBtn != nil {
                        setEnabled(banBtn, ch.valid())
                        if ch.link || !ch.del {
                                banBtn.SetText("Block")
                        } else {
                                banBtn.SetText("Delete")
                        }
                }
        }
        blockChk.OnChanged = func(on bool) { ch.link = on; upd() }
        deleteChk.OnChanged = func(on bool) { ch.del = on; upd() }

        body := container.NewVBox(
                widget.NewLabel(banActionWord(k, shape)+" "+shortBanTarget(clientID)+" on "+core.Def(k).Name+"."),
                widget.NewLabel("This is a connection link - choose what happens to it:"),
                blockChk,
                blockNote,
                reasonRow,
                widget.NewSeparator(),
                deleteChk,
                deleteNote,
                widget.NewSeparator(),
                summary,
        )

        var d *widget.PopUp
        banBtn = dangerButton("Block", func() {
                if !ch.valid() {
                        return
                }
                d.Hide()
                v.runBan(clientID, ch, shape)
        })
        upd()

        d = widget.NewModalPopUp(container.NewVBox(
                widget.NewLabel("Ban this link?"),
                capToWindow(v.c.win, body),
                container.NewHBox(
                        widget.NewButton("Cancel", func() { d.Hide() }),
                        banBtn,
                ),
        ), v.c.win.Canvas())
        d.Show()
}

// banActionWord is the dialog verb per kind: the file server's
// official moderation action is the FILE block (the target is a file
// from a report, not a user identity - its dialog verb stays Block).
// The chat server's umbrella verb is Ban for BOTH target shapes
// (v1.6.33: the link dialog offers the block AND the delete now, so
// its headline matches the user dialog's; the action-specific verb
// lives on the link dialog's confirm button, which follows what is
// ticked - Block or Delete).
func banActionWord(k core.Kind, shape core.TargetShape) string {
        if k == core.KindXFTP {
                return "Block"
        }
        return "Ban"
}

// banTargetWord names what the moderation acts on per kind and target
// shape: the file server moderates a file, the chat server bans a user
// or a link (v1.6.21).
func banTargetWord(k core.Kind, shape core.TargetShape) string {
        if k == core.KindXFTP {
                return "file"
        }
        if shape == core.ShapeLink {
                return "link"
        }
        return "user"
}

// runBan executes the chosen restrictions - every option is one of the
// official moderation commands, and what is sent follows the target's
// shape (v1.6.21): the file server gets the block (with the reason
// that marks the file - never a notice: the file server has no
// uploader identity to notify) and/or the permanent delete; the chat
// server gets the ONE official block command whenever a block option
// is ticked - a USER ban sends it with the notice when "Block link
// creation" is ticked (the reason is always the server default there:
// user bans have no reason row) and may add the permanent delete; a
// LINK ban (v1.6.33: the two-option dialog) sends the block with the
// chosen reason and no notice when its option is ticked, and the
// permanent delete of the queue behind the link when that is - the
// block, the delete, both, or the delete alone.
func (v *adminView) runBan(clientID string, ch banChoices, shape core.TargetShape) {
        k := v.selected
        isXFTP := k == core.KindXFTP
        var notice *core.ClientNotice
        if !isXFTP && ch.withNotice() {
                // the TTL the expiry select chose (0 = no expiry)
                notice = core.NewClientNotice(ch.noticeTTL)
        }
        reason := banReasonToSend(k, shape, ch)
        v.runModeration(func() (string, error) {
                rec := v.c.eng.State.Server(k)
                var sb strings.Builder
                n := 1
                if isXFTP {
                        if ch.block {
                                out, err := core.BanClient(k, rec, clientID, reason, nil)
                                if err != nil {
                                        return "", err
                                }
                                sb.WriteString("Blocked the file - the stored chunks were removed; every\n   further access to the file returns the official BLOCKED error\n   with your reason")
                                sb.WriteString(".\nServer response: " + strings.TrimSpace(out) + "\n")
                                n++
                        }
                } else if ch.block || ch.link || ch.linkCreation {
                        out, err := core.BanClient(k, rec, clientID, reason, notice)
                        if err != nil {
                                return "", err
                        }
                        fmt.Fprintf(&sb, "%d. ", n)
                        if shape == core.ShapeLink {
                                // the link block: new joins see the reason
                                fmt.Fprintf(&sb, "Blocked %s - the link is blocked: it stops working for\n   everybody, and new joins get the official BLOCKED error with your\n   reason. No expiry - it lasts until you Unban (delivered through\n   the official queue block, so the queue's own traffic stops too)", clientID)
                        } else if ch.block {
                                fmt.Fprintf(&sb, "Blocked %s - the client can no longer send or\n   receive through it", clientID)
                                if ch.withNotice() {
                                        fmt.Fprintf(&sb, ", and link creation is blocked -\n   their app refuses to create new links via your server %s", noticeExpiryUntil(ch.noticeTTL))
                                }
                        } else {
                                // link creation alone: the queue block is how the notice travels
                                fmt.Fprintf(&sb, "Blocked %s - link creation is blocked: their app refuses to\n   create new links via your server %s\n   (delivered through the official queue block, so their messages\n   through this queue stop too; undo with Unban)", clientID, noticeExpiryUntil(ch.noticeTTL))
                        }
                        if ch.withNotice() {
                                sb.WriteString(" (ban notice sent)")
                        }
                        sb.WriteString(".\nServer response: " + strings.TrimSpace(out) + "\n")
                        n++
                }
                if ch.del {
                        out, err := core.DeleteClient(k, rec, clientID)
                        if err != nil {
                                sb.WriteString("Delete failed: " + err.Error())
                                return sb.String(), nil
                        }
                        if k == core.KindXFTP {
                                fmt.Fprintf(&sb, "\n%d. Deleted the file outright - clients get a plain no-file error.\nServer response: %s\n", n, strings.TrimSpace(out))
                        } else if shape == core.ShapeLink {
                                fmt.Fprintf(&sb, "\n%d. Deleted the queue behind the link permanently.\nServer response: %s\n", n, strings.TrimSpace(out))
                        } else {
                                fmt.Fprintf(&sb, "\n%d. Deleted the queue permanently.\nServer response: %s\n", n, strings.TrimSpace(out))
                        }
                        n++
                }
                return sb.String(), nil
        })
}

// gateLine is the human summary of the server-wide registration gate.
func gateLine(k core.Kind, blocked bool) string {
        if k == core.KindXFTP {
                if blocked {
                        return "[AUTH] new_files = off: the server now refuses NEW file uploads\n" +
                                "(existing files keep working). The server was restarted to apply it."
                }
                return "[AUTH] new_files = on: new file uploads are allowed again."
        }
        if blocked {
                return "[AUTH] new_queues = off: the server now refuses NEW registrations\n" +
                        "(existing chats keep working). The server was restarted to apply it."
        }
        return "[AUTH] new_queues = on: new registrations are allowed again."
}

// shortBanTarget truncates a moderation target for dialog headlines.
func shortBanTarget(id string) string {
        if len(id) > 24 {
                return id[:24] + "…"
        }
        return id
}

// dangerButton returns a danger-importance button (the destructive
// confirm action inside moderation dialogs).
func dangerButton(label string, fn func()) *widget.Button {
        b := widget.NewButton(label, fn)
        b.Importance = widget.DangerImportance
        return b
}

// capToWindow keeps a dialog body inside the window: Fyne centers a
// modal popup only while its min size fits the canvas - a popup whose
// natural size exceeds the canvas is clamped to the full window and
// stops being centered (the v1.6.17-27 moderation copy grew until that
// happened on laptop-size windows, so every dialog appeared pinned to
// the top-left corner). When the body would not fit it is capped to
// the canvas minus the dialog chrome and scrolls instead, inside a
// popup that is a centered box again. v1.6.28 slimmed the dialogs, so
// this only fires on small windows - it is the safety net.
//
// cappedLayout (v1.6.37) keeps a dialog body as small as its content
// but never larger than max. MinSize-wise it looks THROUGH a scroll
// container: a VScroll's own minimum height is its ~32px chrome floor,
// not the content's (the same trap that collapsed the Tor log card in
// v1.6.36), so the scroll's content requirement is used directly.
type cappedLayout struct{ max fyne.Size }

func (c cappedLayout) MinSize(objects []fyne.CanvasObject) fyne.Size {
        if len(objects) == 0 {
                return fyne.Size{}
        }
        m := objects[0].MinSize()
        if s, ok := objects[0].(*container.Scroll); ok && s.Content != nil {
                m = s.Content.MinSize()
        }
        return fyne.NewSize(
                fyne.Min(m.Width, c.max.Width),
                fyne.Min(m.Height, c.max.Height),
        )
}

func (c cappedLayout) Layout(objects []fyne.CanvasObject, size fyne.Size) {
        for _, o := range objects {
                o.Resize(size)
        }
}

// capToWindow wraps a dialog body so it can never outgrow the window:
// it scrolls inside a box whose minimum size is the body's own minimum,
// clamped to the canvas minus the dialog chrome. v1.6.28 introduced the
// cap for bodies that were too tall ALREADY AT OPEN (laptop-size
// windows had the popup clamped to the full canvas and pinned to the
// top-left corner). But the check ran once, at open time: the ban
// dialog's live "What happens…" summary grows every time a restriction
// is ticked, and a dialog that fit on open silently outgrew the window
// afterwards (Fyne clamps the popup to the full canvas - the question
// row and the buttons ended up off-screen, v1.6.37's report). The
// capped layout re-evaluates on every refresh, so growth past the cap
// turns into scrolling instead of clipping.
func capToWindow(win fyne.Window, body fyne.CanvasObject) fyne.CanvasObject {
        cs := win.Canvas().Size()
        capW, capH := cs.Width-120, cs.Height-200
        if capW < 200 {
                capW = 200
        }
        if capH < 200 {
                capH = 200
        }
        return container.New(cappedLayout{max: fyne.NewSize(capW, capH)}, container.NewVScroll(body))
}

// doUnban removes the ban for the entered client.
func (v *adminView) doUnban() {
        clientID := v.banTarget()
        v.runModeration(func() (string, error) {
                k := v.selected
                rec := v.c.eng.State.Server(k)
                out, err := core.UnbanClient(k, rec, clientID)
                if err != nil {
                        return "", err
                }
                return "Unbanned " + clientID + ".\nServer response: " + strings.TrimSpace(out), nil
        })
}

// doStatus shows the queue status of the entered client.
func (v *adminView) doStatus() {
        clientID := v.banTarget()
        v.runModeration(func() (string, error) {
                k := v.selected
                rec := v.c.eng.State.Server(k)
                return core.ClientStatus(k, rec, clientID)
        })
}

// doDelete permanently deletes the entered client's queue.
func (v *adminView) doDelete() {
        clientID := v.banTarget()
        v.confirmDelete(clientID)
}

// confirmDelete asks before permanently deleting a queue or file.
func (v *adminView) confirmDelete(clientID ...string) {
        id := strings.Join(clientID, "")
        if id == "" {
                id = v.banTarget()
        }
        if err := coreValidClientID(id); err != nil {
                showError(v.c.win, err)
                return
        }
        title := "Delete this queue?"
        msg := "Deleting removes this client's queue and any undelivered\nmessages stored for it, permanently. This cannot be undone."
        result := "Deleted the queue of " + id
        if v.selected == core.KindXFTP {
                title = "Delete this file?"
                msg = "Deleting removes this file and its stored chunks from the\nfile server, permanently - clients then get a plain no-file error,\nwithout a reason (use Block instead to record one). This cannot\nbe undone."
                result = "Deleted the file " + id
        }
        v.confirmModeration(title, msg, func() (string, error) {
                k := v.selected
                rec := v.c.eng.State.Server(k)
                out, err := core.DeleteClient(k, rec, id)
                if err != nil {
                        return "", err
                }
                return result + ".\nServer response: " + strings.TrimSpace(out), nil
        })
}

// coreValidClientID mirrors the core-side validation for a friendly
// pre-check before opening dialogs.
func coreValidClientID(clientID string) error {
        if strings.TrimSpace(clientID) == "" {
                return fmt.Errorf("no client ID entered - paste the client's queue or link ID, or the invitation link from the report")
        }
        return nil
}

// runModeration runs a moderation command off-thread, shows the outcome in
// the moderation result area and refreshes the panel. It uses its own
// busyMod flag: a concurrent stats refresh used to swallow moderation
// clicks silently (the panel stayed busy for seconds because every
// control-port command cost a full 4s read deadline) - clicks on
// Ban/Unban/Delete simply did nothing.
func (v *adminView) runModeration(fn func() (string, error)) {
        if v.busyMod {
                v.banResult.SetText("One moment - the previous moderation command is still running.")
                return
        }
        v.busyMod = true
        v.banResult.SetText("Talking to the server…")
        go func() {
                out, err := fn()
                fyne.Do(func() {
                        v.busyMod = false
                        if err != nil {
                                v.banResult.SetText(err.Error())
                                showError(v.c.win, err)
                                return
                        }
                        v.banResult.SetText(strings.TrimSpace(out))
                })
                if err == nil {
                        _, _ = v.fetchAll()
                }
        }()
}

// renderBans draws the bans the SERVER itself holds - the store log
// journals every control-port block with the full official blocking info
// (reason and ban notice), so this is the server's record, not a local
// memory: bans placed outside sxops appear here too. The journal carries
// no timestamps, so there is no "placed" column. Derived from the same
// parsed store log as the queue list (v.lastQueues).
func (v *adminView) renderBans() {
        if v.lastQueues == nil {
                v.bansCnt.SetText("(the ban list appears once " + core.Def(v.selected).Name + " is running)")
                v.bansBox.Objects = nil
                v.bansBox.Refresh()
                return
        }
        var banned []core.QueueInfo
        for _, q := range v.lastQueues {
                if q.Blocked {
                        banned = append(banned, q)
                }
        }
        if len(banned) == 0 {
                v.bansCnt.SetText("No bans recorded on this server.")
                v.bansBox.Objects = nil
                v.bansBox.Refresh()
                return
        }
        v.bansCnt.SetText(fmt.Sprintf("%d banned", len(banned)))
        idHead := "client"
        if v.selected == core.KindXFTP {
                idHead = "file"
        }
        header := container.NewHBox(
                cell(idHead, 240),
                cell("reason", 100),
                cell("notice", 110),
                cell("", 90),
        )
        var objs []fyne.CanvasObject
        objs = append(objs, header, widget.NewSeparator())
        for _, b := range banned {
                id := b.ID
                if len(id) > 24 {
                        id = id[:24] + "…"
                }
                reason := b.BlockReason
                if reason != core.BanReasonContent {
                        reason = "spam"
                }
                var act *widget.Button
                if v.selected == core.KindXFTP {
                        // file-server bans cannot be undone - offer deletion instead
                        act = widget.NewButton("Delete", func() {
                                v.banIDEntry.SetText(b.ID)
                                v.confirmDelete(b.ID)
                        })
                        act.Importance = widget.DangerImportance
                } else {
                        act = widget.NewButton("Unban", func() {
                                v.banIDEntry.SetText(b.ID)
                                v.doUnban()
                        })
                }
                objs = append(objs, container.NewHBox(
                        monoCell(id, 240),
                        cell(reason, 100),
                        cell(banNoticeInfo(b.BlockNotice, b.BlockNoticeTTL), 110),
                        container.NewGridWrap(fyne.NewSize(90, act.MinSize().Height), act),
                ))
        }
        v.bansBox.Objects = objs
        v.bansBox.Refresh()
}

// banNoticeInfo renders the ban notice the server recorded with a block:
// "-" (no notice was sent), "no expiry" or the lifetime in the same
// terms the ban form offers (the protocol journals the raw seconds).
func banNoticeInfo(notice bool, ttl int64) string {
        if !notice {
                return "-"
        }
        if ttl == 0 {
                return "no expiry"
        }
        if ttl%3600 == 0 && ttl < 86400 {
                return fmt.Sprintf("%d hours", ttl/3600)
        }
        days := ttl / 86400
        switch days {
        case 1:
                return "1 day"
        case 365:
                return "1 year"
        }
        if days > 0 {
                return fmt.Sprintf("%d days", days)
        }
        return fmt.Sprintf("%d seconds", ttl)
}

// setGateUI renders the server-wide registration-gate card: hidden when
// the selected server has no gate (NTF) or its INI could not be read.
// v1.6.39: the file server's copy says UPLOADS, not registrations - the
// same [AUTH] gate, but what it stops on the file server is new file
// uploads (the official method, new_files = off).
func (v *adminView) setGateUI(blocked, ok bool) {
        if !ok || !core.NewLinksSupported(v.selected) {
                v.gateCard.Hide()
                return
        }
        if v.selected == core.KindXFTP {
                if blocked {
                        v.gateLbl.SetText("Current state: NEW UPLOADS REFUSED (new_files = off).\n" +
                                "Existing files keep working; nobody can upload new files to this\n" +
                                "server until you allow it again.")
                        v.gateBtn.SetText("Allow new file uploads")
                } else {
                        v.gateLbl.SetText("Current state: uploads allowed (new_files = on).")
                        v.gateBtn.SetText("Refuse new file uploads")
                }
                if blocked {
                        v.gateBtn.Importance = widget.HighImportance
                } else {
                        v.gateBtn.Importance = widget.DangerImportance
                }
                v.gateBtn.OnTapped = func() { v.toggleGate(!blocked) }
                v.gateBtn.Refresh()
                v.gateCard.Show()
                return
        }
        if blocked {
                v.gateLbl.SetText("Current state: NEW REGISTRATIONS REFUSED (new_queues = off).\n" +
                        "Existing chats keep working; nobody can register new chats, addresses or uploads until you allow it again.")
                v.gateBtn.SetText("Allow new registrations again")
                v.gateBtn.Importance = widget.HighImportance
                v.gateBtn.OnTapped = func() { v.toggleGate(false) }
        } else {
                v.gateLbl.SetText("Current state: allowed (new_queues = on).")
                v.gateBtn.SetText("Refuse new registrations")
                v.gateBtn.Importance = widget.DangerImportance
                v.gateBtn.OnTapped = func() { v.toggleGate(true) }
        }
        v.gateBtn.Refresh()
        v.gateCard.Show()
}

// toggleGate flips the server-wide [AUTH] gate after a confirmation
// (the change restarts the server when it is running). v1.6.39: the
// file server's confirmation talks about file uploads.
func (v *adminView) toggleGate(blocked bool) {
        k := v.selected
        msg := "Turning this on restarts " + core.Def(k).Name + " and the server then refuses ALL new registrations - nobody can create new chats, contact addresses or group invites through it (existing chats keep working). This is the official SimpleX anti-spam measure; there is no per-client variant."
        if !blocked {
                msg = "New registrations will be allowed again on " + core.Def(k).Name + " (restarts the server to apply)."
        }
        if k == core.KindXFTP {
                msg = "Turning this on restarts the XFTP server and it then refuses ALL new file uploads - nobody can send new files through it until you allow it again (existing files keep working). This is the official SimpleX method ([AUTH] new_files = off); there is no per-client variant."
                if !blocked {
                        msg = "New file uploads will be allowed again on the XFTP server (restarts it to apply)."
                }
        }
        title := "Change the registration gate?"
        if k == core.KindXFTP {
                title = "Change the file uploads gate?"
        }
        v.confirmModeration(title, msg, func() (string, error) {
                if err := v.c.eng.SetNewLinksBlocked(k, blocked); err != nil {
                        return "", err
                }
                return gateLine(k, blocked), nil
        })
}

// xftpBanPlaceholder is the File ID field's hint for the file server:
// the files list card sits ABOVE the moderation card in the admin
// panel (v1.6.43 fixed the direction - it used to say "below").
func xftpBanPlaceholder() string { return "file ID from the list above" }

// setBanCardUI switches the moderation card's title, copy, field hint
// and button labels to the selected server kind: the chat server bans
// chat queues, the file server blocks FILES (the moderation target of
// a file report). Must run on the UI thread.
func (v *adminView) setBanCardUI(k core.Kind) {
        if k == core.KindXFTP {
                v.banHead.SetText("Block a file from the server")
                v.banSub.SetText(banCardCopy(k))
                v.banIDEntry.PlaceHolder = xftpBanPlaceholder()
                v.banIDLabel.SetText("File ID:")
                v.banBtn.SetText("Block file")
                v.unbanBtn.SetText("Unban")
                v.banStatusBtn.SetText("Check status")
                v.banDeleteBtn.SetText("Delete file")
        } else {
                v.banHead.SetText("Ban a client from the server")
                v.banSub.SetText(banCardCopy(k))
                v.banIDEntry.PlaceHolder = "queue / link ID, or a pasted invitation / group link (https:// or simplex:/)"
                v.banIDLabel.SetText("Client ID or link:")
                v.banBtn.SetText("Ban client")
                v.unbanBtn.SetText("Unban")
                v.banStatusBtn.SetText("Check status")
                v.banDeleteBtn.SetText("Delete queue")
        }
        v.banIDEntry.Refresh()
        v.banIDLabel.Refresh()
        v.banBtn.Refresh()
        v.banDeleteBtn.Refresh()
}

// setReportsUI renders the Abuse reports card: hidden for the kinds
// that do not publish complaints contacts (NTF) or when the INI could
// not be read. The entries mirror the INI values - a manual Refresh
// reloads them (edits in the fields are not kept on refresh).
// Must run on the UI thread.
func (v *adminView) setReportsUI(k core.Kind, simplex, email string, err error, ok bool) {
        if !ok || !core.ComplaintsSupported(k) {
                v.reportsCard.Hide()
                return
        }
        v.reportsEntry.SetText(simplex)
        v.reportsEmail.SetText(email)
        if simplex != "" {
                line := "Configured: the address above is published on the\n" +
                        "info web page of " + core.Def(k).Name + " (https://your-server).\n" +
                        "Users copy it into their app and message you - the reports\n" +
                        "arrive as ordinary chats, answer them from your SimpleX app."
                if email != "" {
                        line += "\nEmail: " + email
                }
                // the INI may hold an address the server refuses (e.g. a
                // hand-edited one, or a one-time link from before v1.6.19):
                // the server does not start with it - warn and offer the fix
                if verr := core.ValidateComplaintsSimplex(simplex); verr != nil {
                        v.reportsHint.SetText("WARNING - the configured address is invalid and\n" +
                                core.Def(k).Name + " cannot start with it:\n" + verr.Error() + "\n" +
                                "Fix or clear it below and save - the server then starts again.")
                } else {
                        v.reportsHint.SetText("")
                }
                v.reportsCur.SetText(line)
        } else {
                v.reportsHint.SetText("No complaints address is configured - the info page shows\n" +
                        "no contact, so users have no way to reach you. Create your SimpleX\n" +
                        "address in the app (profile -> SimpleX address) - a durable contact\n" +
                        "address, NOT a one-time link - and paste it above.")
                v.reportsCur.SetText("")
        }
        v.reportsCard.Show()
}

// doSaveComplaints validates the form and writes the complaints
// contacts to the server INI ([INFORMATION] complaints_simplex /
// complaints_email), restarting the server when it is running so the
// info web page picks the address up right away.
func (v *adminView) doSaveComplaints() {
        k := v.selected
        simplex := strings.TrimSpace(v.reportsEntry.Text)
        email := strings.TrimSpace(v.reportsEmail.Text)
        if err := core.ValidateComplaintsSimplex(simplex); err != nil {
                showError(v.c.win, err)
                return
        }
        if err := core.ValidateComplaintsEmail(email); err != nil {
                showError(v.c.win, err)
                return
        }
        msg := "Saving the report address writes [INFORMATION] complaints_simplex into\n" +
                core.Def(k).Name + "'s INI and restarts the server when it is running,\n" +
                "so the address appears on its info web page. Users copy it from\n" +
                "the page into their app and message you - answer from your app."
        if simplex == "" && email == "" {
                msg = "This removes the complaints contacts from " + core.Def(k).Name + "'s INI\n" +
                        "(restarts the server when it is running). The info page then shows\n" +
                        "no contact - users have no way to reach you."
        }
        v.confirmModeration("Save the report address?", msg, func() (string, error) {
                if err := v.c.eng.SetComplaints(k, simplex, email); err != nil {
                        return "", err
                }
                if simplex == "" && email == "" {
                        return "Complaints contacts removed. The INI applies at the next start\n" +
                                "(the server was restarted now when it was running).", nil
                }
                out := "Saved. Your address is published on the info web page of " +
                        core.Def(k).Name + ".\n"
                if form := core.ComplaintsSimplexForm(simplex); form != "" {
                        out += "Recognized as: " + form + ".\n"
                }
                out += "Users copy it from the page into their app and message you;\n" +
                        "answer them from your SimpleX app, then act with the moderation tools above."
                if email != "" {
                        out += "\nEmail complaint address: " + email
                }
                return out, nil
        })
}

// runAdmin executes one admin control-port command.
func (v *adminView) runAdmin(command, arg string) (string, error) {
        k := v.selected
        rec := v.c.eng.State.Server(k)
        out, err := core.FetchRaw(k, rec, command, arg)
        if err != nil {
                return "", err
        }
        return strings.TrimSpace(out), nil
}

// runAsync runs a fetch/command fn off-thread and shows the result (or
// an error dialog). Refreshes the panel afterwards.
func (v *adminView) runAsync(fn func() (string, error)) {
        if v.busy {
                return
        }
        v.busy = true
        go func() {
                defer func() { fyne.Do(func() { v.busy = false }) }()
                out, err := fn()
                if err != nil {
                        showError(v.c.win, err)
                        return
                }
                if out != "" {
                        v.c.glog.Detail(fmt.Sprintf("[admin] %s: %s", v.selected, strings.ReplaceAll(out, "\n", " | ")))
                        fyne.Do(func() { v.statLbl.SetText(out) })
                }
        }()
}

// confirmModeration asks before running a state-changing moderation
// command; the result appears in the moderation result area.
// The pop-up is hidden the moment the user confirms - it used to stay
// open on top of the result area while the command ran in the
// background, which looked like a frozen dialog (and further clicks on
// "Yes, do it" were swallowed by the in-flight guard).
func (v *adminView) confirmModeration(title, msg string, fn func() (string, error)) {
        l := widget.NewLabel(msg)
        l.Wrapping = fyne.TextWrapWord
        var d *widget.PopUp
        d = widget.NewModalPopUp(container.NewVBox(
                widget.NewLabel(title),
                capToWindow(v.c.win, l),
                container.NewHBox(
                        widget.NewButton("Cancel", func() { d.Hide() }),
                        widget.NewButton("Yes, do it", func() {
                                d.Hide()
                                v.runModeration(fn)
                        }),
                ),
        ), v.c.win.Canvas())
        d.Show()
}
