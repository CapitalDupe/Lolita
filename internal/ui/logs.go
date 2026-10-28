package ui

import (
        "os"
        "strings"
        "time"

        "fyne.io/fyne/v2"
        "fyne.io/fyne/v2/container"
        "fyne.io/fyne/v2/theme"
        "fyne.io/fyne/v2/widget"

        "sxops/internal/core"
)

// logsView shows the tail of each component's log file.
//
// For a zero-experience user this is read-only: logs are there when
// something goes wrong and someone asks for them. Saving to a file is
// the one action available.
type logsView struct {
        c    *console
        root fyne.CanvasObject

        current string

        entry   *widget.Entry
        hint    *widget.Label
        fetched time.Time
}

func newLogsView(c *console) *logsView {
        v := &logsView{c: c, current: "smp"}

        v.entry = widget.NewMultiLineEntry()
        v.entry.TextStyle = fyne.TextStyle{Monospace: true}
        // wrapped so long log lines cannot stretch the window wider
        // than the screen (the entry scrolls internally when wrapping)
        v.entry.Wrapping = fyne.TextWrapBreak
        // 16 visible rows is the floor; as the Border center below it
        // fills the whole view height. NOTE: the entry must NOT be
        // wrapped in container.NewVScroll - that collapses it to the
        // scroll container's own ~32px minimum; a wrapping multi-line
        // entry already scrolls vertically on its own.
        v.entry.SetMinRowsVisible(16)
        v.entry.Disable()
        v.entry.SetText("(no log yet)")

        v.hint = widget.NewLabel("Logs update automatically while this page is open.")
        v.hint.TextStyle = fyne.TextStyle{Italic: true}
        v.hint.Wrapping = fyne.TextWrapWord

        tabs := container.NewVBox()
        for _, choice := range []struct{ label, id string }{
                {"SMP server", "smp"}, {"XFTP server", "xftp"}, {"NTF server", "ntf"},
                {"Chat relay", "relay"}, {"Tor", "tor"},
        } {
                id := choice.id
                b := widget.NewButton(choice.label, func() {
                        v.current = id
                        v.hint.SetText("Loading " + choice.label + " log…")
                        v.entry.SetText("")
                        v.fetchNow()
                })
                b.Alignment = widget.ButtonAlignLeading
                tabs.Add(b)
        }

        save := widget.NewButtonWithIcon("Save log to a file", theme.DocumentSaveIcon(), func() { v.saveLog() })
        refresh := widget.NewButtonWithIcon("Refresh now", theme.ViewRefreshIcon(), func() { v.fetchNow() })

        v.root = container.NewBorder(
                nil, nil,
                container.NewVBox(
                        card("Which log?", "", tabs),
                ),
                nil,
                // top: hint + buttons; center: the log pane fills the rest
                container.NewPadded(container.NewBorder(
                        container.NewVBox(v.hint, container.NewHBox(refresh, save)),
                        nil, nil, nil,
                        v.entry,
                )),
        )
        return v
}

// refresh is called by the poll loop while the view is visible.
func (v *logsView) refresh() {
        if time.Since(v.fetched) < 3*time.Second {
                return
        }
        v.fetchNow()
}

// fetchNow reads the tail of the current log off-thread.
func (v *logsView) fetchNow() {
        v.fetched = time.Now()
        go func() {
                text, err := v.readCurrent()
                fyne.Do(func() {
                        if err != nil {
                                v.hint.SetText("Log not available: " + err.Error())
                                v.entry.SetText("")
                                return
                        }
                        hint := "Newest lines are at the bottom. " +
                                "If someone asks you to 'share the logs', use Save log to a file."
                        if v.hint.Text != hint {
                                v.hint.SetText(hint)
                        }
                        setLogText(v.entry, text)
                })
        }()
}

// setLogText replaces the text of a read-only log entry and keeps the
// view at the BOTTOM, where the newest lines are. Fyne resets an
// entry's scroll to the top every time SetText swaps the content, so a
// live log that refreshes every few seconds constantly yanks the reader
// back up. Park the cursor on the last line instead: Entry.Refresh()
// then runs moveCursor() -> ensureCursorVisible(), which scrolls the
// entry's internal scroller down to the cursor. When the text has not
// changed the call is a no-op, so a reader who scrolled up to older
// lines is left in peace until something new actually arrives.
func setLogText(e *widget.Entry, text string) {
        if e.Text == text {
                return
        }
        e.SetText(text)
        e.CursorRow, e.CursorColumn = endCursor(text)
        e.Refresh()
}

// endCursor returns the row/column (column in runes) of the very end
// of text - the position that scrolls into view via ensureCursorVisible.
func endCursor(text string) (row, col int) {
        if text == "" {
                return 0, 0
        }
        if nl := strings.LastIndexByte(text, '\n'); nl >= 0 {
                return strings.Count(text, "\n"), len([]rune(text[nl+1:]))
        }
        return 0, len([]rune(text))
}

// readCurrent returns the last ~400 lines of the selected log.
func (v *logsView) readCurrent() (string, error) {
        var (
                data []byte
                err  error
        )
        switch v.current {
        case "smp", "xftp", "ntf", "relay":
                k := core.Kind(v.current)
                if !v.c.eng.State.Server(k).Installed {
                        return "No log yet - " + nameOf(v.current) + " is not installed.", nil
                }
                if k == core.KindRelay {
                        data, err = os.ReadFile(v.c.eng.Paths.ProcLogFile(k))
                        if err == nil {
                                data = []byte(lastLines(string(data), 400))
                        }
                } else {
                        data, err = v.c.eng.Backend.ServerLogTail(k, 400)
                }
        case "tor":
                data, err = os.ReadFile(v.c.eng.Paths.TorLogFile())
                if err == nil {
                        data = []byte(lastLines(string(data), 400))
                }
        default:
                return "", nil
        }
        if err != nil {
                if os.IsNotExist(err) {
                        return "No log yet - it is created when the component starts.", nil
                }
                return "", err
        }
        s := strings.TrimRight(string(data), "\n")
        if s == "" {
                return "(log is empty)", nil
        }
        return s, nil
}

// saveLog writes the currently shown log to a file the user picks.
// The dialog opens through showSaveDialogQuiet (filedialog.go): no
// folders are created anywhere, and the harmless "does not define a
// <NAME> folder" log noise Fyne prints on minimal installs is
// muffled while the dialog is built.
func (v *logsView) saveLog() {
        text := v.entry.Text
        if strings.TrimSpace(text) == "" || strings.HasPrefix(text, "(no log") {
                v.fetchNow()
                return
        }
        name := "sxops-" + v.current + ".log"
        showSaveDialogQuiet(v.c.win, name, func(wr fyne.URIWriteCloser, err error) {
                if err != nil || wr == nil {
                        return
                }
                defer wr.Close()
                _, _ = wr.Write([]byte(text))
                v.hint.SetText("Log saved.")
        })
}

func nameOf(id string) string {
        switch id {
        case "smp":
                return "the SMP server"
        case "xftp":
                return "the XFTP server"
        case "ntf":
                return "the NTF server"
        case "relay":
                return "the chat relay"
        }
        return "the component"
}

// lastLines returns the last n lines of s.
func lastLines(s string, n int) string {
        s = strings.TrimRight(s, "\n")
        if s == "" {
                return ""
        }
        lines := strings.Split(s, "\n")
        if len(lines) <= n {
                return s
        }
        return strings.Join(lines[len(lines)-n:], "\n")
}
