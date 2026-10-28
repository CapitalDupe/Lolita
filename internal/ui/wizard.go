package ui

import (
	"context"
	"fmt"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"sxops/internal/core"
)

// wizardView is the guided one-button setup.
type wizardView struct {
	c    *console
	root fyne.CanvasObject

	relayName *widget.Entry
	usePw     *widget.Check
	pw        *widget.Entry

	optBox   *fyne.Container
	startBtn *widget.Button

	running  bool
	stepLbl  *widget.Label
	prog     *widget.ProgressBarInfinite
	logEntry *widget.Entry
	progBox  *fyne.Container

	resultBox *fyne.Container
}

func newWizardView(c *console) *wizardView {
	v := &wizardView{c: c}
	s := c.eng.State.Settings()

	v.relayName = widget.NewEntry()
	v.relayName.SetText(s.RelayName)
	v.pw = widget.NewPasswordEntry()
	v.usePw = widget.NewCheck("Protect the server with a password (recommended)", func(ok bool) {
		if ok {
			v.pw.Show()
		} else {
			v.pw.Hide()
		}
	})
	if s.PasswordProtect {
		v.usePw.SetChecked(true)
		v.pw.SetText(s.QueuePassword)
	}

	v.startBtn = widget.NewButtonWithIcon("Set up my SimpleX server", theme.MediaPlayIcon(), func() { v.run() })
	v.startBtn.Importance = widget.HighImportance

	v.stepLbl = widget.NewLabel("")
	v.stepLbl.Hide()
	v.prog = widget.NewProgressBarInfinite()
	v.prog.Hide()
	v.logEntry = widget.NewMultiLineEntry()
	v.logEntry.TextStyle = fyne.TextStyle{Monospace: true}
	v.logEntry.Wrapping = fyne.TextWrapOff
	v.logEntry.Disable()
	v.logEntry.Hide()
	v.progBox = container.NewVBox(v.stepLbl, v.prog, v.logEntry)
	v.resultBox = container.NewVBox()

	intro := card("Welcome to sxops", "", container.NewVBox(
		richLines([]string{
			"sxops sets up your own private SimpleX Chat server in a few minutes.",
			"",
			"It will:",
			"   1. Download the official SimpleX server programs (about 80 MB)",
			"   2. Create all configuration, keys and addresses automatically",
			"   3. Connect through the Tor network so your server works without",
			"      any port forwarding or a public IP address",
			"   4. Start everything and show you the addresses to paste into",
			"      the SimpleX app on your phone or computer",
			"",
			"You do not need to configure anything - just press the button below.",
		}),
	))

	options := widget.NewAccordion(widget.NewAccordionItem("Optional settings", container.NewVBox(
		widget.NewLabel("Relay display name (what your contacts see):"),
		v.relayName,
		v.usePw,
		v.pw,
	)))
	v.optBox = container.NewVBox(options)

	preflight := container.NewVBox()
	go v.runPreflight(preflight)

	v.root = container.NewVScroll(container.NewVBox(
		intro,
		v.startBtn,
		v.progBox,
		v.resultBox,
		preflight,
		v.optBox,
	))
	return v
}

// runPreflight fills the preflight checklist (async, called once).
func (v *wizardView) runPreflight(box *fyne.Container) {
	checks := core.Doctor(v.c.eng.Paths)
	fyne.Do(func() {
		var rows []fyne.CanvasObject
		for _, ch := range checks {
			var icon fyne.Resource
			state := "OK"
			if ch.OK {
				icon = theme.NewSuccessThemedResource(theme.ConfirmIcon())
			} else {
				icon = theme.NewErrorThemedResource(theme.ErrorIcon())
				state = "needs attention"
			}
			l := widget.NewLabel(fmt.Sprintf("%s: %s — %s", ch.Name, state, ch.Detail))
			l.Wrapping = fyne.TextWrapWord
			rows = append(rows, container.NewHBox(
				widget.NewIcon(icon),
				l,
			))
			if ch.FixHint != "" && !ch.OK {
				h := widget.NewLabel("   fix: " + ch.FixHint)
				h.Wrapping = fyne.TextWrapWord
				rows = append(rows, h)
			}
		}
		// Tor missing is not fatal for setup (soft-fail), mark it visually
		box.Add(card("Pre-flight checks", "These are checked automatically before anything runs.",
			container.NewVBox(rows...)))
		box.Refresh()
	})
}

// run starts the full setup in the background.
func (v *wizardView) run() {
	if v.running {
		return
	}
	v.running = true
	defer func() { v.running = false }()

	// persist chosen options
	s := v.c.eng.State.Settings()
	s.RelayName = strings.TrimSpace(v.relayName.Text)
	if s.RelayName == "" {
		s.RelayName = "My Relay"
	}
	s.PasswordProtect = v.usePw.Checked
	s.QueuePassword = v.pw.Text
	_ = v.c.eng.State.SetSettings(s)

	v.startBtn.Disable()
	v.startBtn.SetText("Setting up…")
	v.optBox.Hide()
	v.stepLbl.SetText("Starting (this can take a few minutes)")
	v.stepLbl.Show()
	v.prog.Start()
	v.prog.Show()
	v.resultBox.Objects = nil
	v.resultBox.Refresh()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	go func() {
		defer cancel()
		opts := core.SetupOptions{
			EnableSMP:       true,
			EnableXFTP:      true,
			EnableRelay:     true,
			AllowTorFailure: true,
		}
		warnings, err := v.c.eng.FullSetup(ctx, opts)
		fyne.Do(func() { v.finish(warnings, err) })
	}()
}

// appendLog shows the tail of the install log (called from poll loop).
func (v *wizardView) appendLog(lines []string) {
	if !v.running || len(lines) == 0 {
		return
	}
	if len(lines) > 30 {
		lines = lines[len(lines)-30:]
	}
	v.logEntry.SetText(strings.Join(lines, "\n"))
	if !v.logEntry.Visible() {
		v.logEntry.Show()
	}
	if len(lines) > 0 {
		v.stepLbl.SetText(lastStep(lines))
	}
	v.progBox.Refresh()
}

func lastStep(lines []string) string {
	for i := len(lines) - 1; i >= 0; i-- {
		if strings.HasPrefix(lines[i], "» ") {
			return strings.TrimPrefix(lines[i], "» ")
		}
	}
	return ""
}

// finish renders the setup result.
func (v *wizardView) finish(warnings []string, err error) {
	v.prog.Stop()
	v.prog.Hide()
	v.stepLbl.Hide()

	if err != nil {
		errText := widget.NewLabel(err.Error())
		errText.Wrapping = fyne.TextWrapWord
		retry := widget.NewButtonWithIcon("Try again", theme.MediaReplayIcon(), func() {
			v.resultBox.Objects = nil
			v.startBtn.Enable()
			v.startBtn.SetText("Set up my SimpleX server")
			v.optBox.Show()
			v.resultBox.Refresh()
		})
		retry.Importance = widget.HighImportance
		viewLog := widget.NewButton("Show full log", func() {
			lines := v.c.glog.snapshot(false)
			e := widget.NewMultiLineEntry()
			e.TextStyle = fyne.TextStyle{Monospace: true}
			e.Wrapping = fyne.TextWrapOff
			e.SetText(strings.Join(lines, "\n"))
			e.Disable()
			dlg := dialog.NewCustom("Setup log", "Close", container.NewVScroll(e), v.c.win)
			dlg.Resize(fyne.NewSize(700, 500))
			dlg.Show()
		})
		head := widget.NewLabel("Setup could not be completed")
		head.TextStyle = fyne.TextStyle{Bold: true}
		v.resultBox.Objects = []fyne.CanvasObject{
			container.NewVBox(
				container.NewHBox(widget.NewIcon(theme.NewErrorThemedResource(theme.ErrorIcon())), head),
				errText,
				container.NewHBox(retry, viewLog),
			),
		}
		v.resultBox.Refresh()
		return
	}

	// success
	var rows []fyne.CanvasObject
	eng := v.c.eng
	addrCount := 0
	for _, st := range eng.Statuses() {
		if st.Address != "" && !strings.Contains(st.Address, "YOUR-SERVER-HOST") {
			addrCount++
		}
	}
	if addrCount > 0 {
		rows = append(rows, widget.NewLabel("Your SimpleX server is ready. These are its addresses -"))
		rows = append(rows, widget.NewLabel("paste them into the SimpleX app (or press QR and scan)."))
		rows = append(rows, widget.NewSeparator())
		for _, st := range eng.Statuses() {
			if st.Address == "" || strings.Contains(st.Address, "YOUR-SERVER-HOST") {
				continue
			}
			rows = append(rows, v.addressRow(st))
		}
	} else {
		// Everything is set up, but Tor has not connected/published yet:
		// show what is running plus clear guidance instead of a blank list.
		head := widget.NewLabel("The servers are installed and running, but Tor is not connected yet -")
		head.Wrapping = fyne.TextWrapWord
		head2 := widget.NewLabel("your addresses appear as soon as it connects.")
		head2.Wrapping = fyne.TextWrapWord
		rows = append(rows, head, head2, widget.NewSeparator())
		for _, st := range eng.Statuses() {
			if st.Optional {
				continue
			}
			name := widget.NewLabel(st.Name)
			name.TextStyle = fyne.TextStyle{Bold: true}
			state := widget.NewLabel(st.StateText)
			rows = append(rows, container.NewHBox(name, widget.NewLabel("-"), state))
		}
		guide := widget.NewLabel("Next step: open the Tor network tab (left) and press Retry connection.\n" +
			"If it keeps failing, that page also shows the reason and a Tor log you can share.")
		guide.Wrapping = fyne.TextWrapWord
		guide.TextStyle = fyne.TextStyle{Italic: true}
		rows = append(rows, guide)
	}
	for _, w := range warnings {
		l := widget.NewLabel("Note: " + w)
		l.Wrapping = fyne.TextWrapWord
		l.TextStyle = fyne.TextStyle{Italic: true}
		rows = append(rows, l)
	}
	open := widget.NewButtonWithIcon("Open the dashboard", theme.HomeIcon(), func() {
		v.c.show("dashboard")
	})
	open.Importance = widget.HighImportance
	rows = append(rows, open)

	head := widget.NewLabel("Setup complete")
	head.TextStyle = fyne.TextStyle{Bold: true}
	v.resultBox.Objects = []fyne.CanvasObject{
		container.NewVBox(
			container.NewHBox(widget.NewIcon(theme.NewSuccessThemedResource(theme.ConfirmIcon())), head),
			container.NewVBox(rows...),
		),
	}
	v.resultBox.Refresh()
	v.startBtn.SetText("Set up again (safe - skips what is already done)")
	v.startBtn.Enable()
}

// addressRow renders one address with copy and QR buttons.
func (v *wizardView) addressRow(st core.ComponentStatus) fyne.CanvasObject {
	name := widget.NewLabel(st.Name)
	name.TextStyle = fyne.TextStyle{Bold: true}
	addr := monoLabel(st.Address)
	row := container.NewBorder(
		nil, nil,
		container.NewVBox(name),
		container.NewHBox(
			copyButton(v.c.win, func() string { return st.Address }),
			widget.NewButtonWithIcon("QR", theme.ViewFullScreenIcon(), func() {
				showQR(v.c.win, st.Name+" address", st.Address)
			}),
		),
		addr,
	)
	return container.NewVBox(widget.NewSeparator(), row)
}

// richLines builds a left-aligned label block.
func richLines(lines []string) fyne.CanvasObject {
	var objs []fyne.CanvasObject
	for _, l := range lines {
		lb := widget.NewLabel(l)
		lb.Wrapping = fyne.TextWrapWord
		objs = append(objs, lb)
	}
	return container.NewVBox(objs...)
}
