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

// updatesView is the dedicated "Updates" section: it compares the
// installed version of every managed component with the latest official
// release on GitHub and installs updates with one click (stop, swap the
// binary, start again - data and addresses are kept).
type updatesView struct {
	c    *console
	root fyne.CanvasObject

	list         *fyne.Container
	checkBtn     *widget.Button
	updateAllBtn *widget.Button
	lastCheck    *widget.Label

	rows      map[core.Kind]*updateRow
	busy      bool
	autoCheck bool // one automatic check per session
}

// updateRow is the per-component line in the updates list.
type updateRow struct {
	name *widget.Label
	desc *widget.Label
	dot  *statusDot
	ver  *widget.Label // "you have vX · latest vY"
	btn  *widget.Button
	obj  fyne.CanvasObject
}

func newUpdatesView(c *console) *updatesView {
	v := &updatesView{c: c, rows: map[core.Kind]*updateRow{}}

	v.checkBtn = widget.NewButtonWithIcon("Check for updates", theme.ViewRefreshIcon(), func() { v.checkUpdates() })
	v.checkBtn.Importance = widget.HighImportance
	v.updateAllBtn = widget.NewButtonWithIcon("Update all", theme.DownloadIcon(), func() { v.updateAll() })
	v.updateAllBtn.Importance = widget.DangerImportance
	v.updateAllBtn.Hide()

	v.lastCheck = widget.NewLabel("Not checked yet - press \"Check for updates\".")
	v.lastCheck.TextStyle = fyne.TextStyle{Italic: true}
	v.lastCheck.Wrapping = fyne.TextWrapWord

	v.list = container.NewVBox()
	v.list.Objects = []fyne.CanvasObject{notInstalledHint()}

	v.root = container.NewVScroll(container.NewVBox(
		card("Keep SimpleX up to date",
			"The SimpleX developers publish new versions regularly - updates fix bugs\n"+
				"and improve security. sxops downloads the official release binaries from\n"+
				"GitHub and installs them for you. An update briefly stops the component,\n"+
				"swaps in the new version and starts it again - your data, keys and server\n"+
				"addresses are kept. Everything keeps working on the old version until\n"+
				"you choose to update.",
			container.NewVBox(
				container.NewHBox(v.checkBtn, v.updateAllBtn),
				v.lastCheck,
				v.list,
			)),
	))
	return v
}

func notInstalledHint() fyne.CanvasObject {
	l := widget.NewLabel("Nothing is installed yet - run the setup first\n(Welcome → Set up my SimpleX server).")
	l.Wrapping = fyne.TextWrapWord
	l.TextStyle = fyne.TextStyle{Italic: true}
	return l
}

// refresh auto-checks once per session when the section is opened.
func (v *updatesView) refresh() {
	if !v.c.eng.State.Get().Provision {
		return // first run: the wizard owns the flow
	}
	if v.autoCheck {
		return
	}
	v.autoCheck = true
	v.checkUpdates()
}

// checkUpdates queries GitHub for each installed component and renders
// one row per component: current version, latest version and an Update
// button when a newer release exists.
func (v *updatesView) checkUpdates() {
	if v.busy {
		return
	}
	v.busy = true
	fyne.Do(func() {
		v.checkBtn.Disable()
		v.updateAllBtn.Hide()
		var rows []fyne.CanvasObject
		v.rows = map[core.Kind]*updateRow{}
		for _, k := range []core.Kind{core.KindSMP, core.KindXFTP, core.KindNTF, core.KindRelay} {
			rec := v.c.eng.State.Server(k)
			if !rec.Installed {
				continue
			}
			rows = append(rows, v.newRow(k))
		}
		if len(rows) == 0 {
			rows = append(rows, notInstalledHint())
		}
		v.list.Objects = rows
		v.list.Refresh()
	})
	go func() {
		defer fyne.Do(func() {
			v.busy = false
			v.checkBtn.Enable()
			v.lastCheck.SetText("Last checked: " + time.Now().Format("15:04"))
		})
		ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
		defer cancel()
		infos := v.c.eng.CheckUpdates(ctx)
		any := false
		for _, info := range infos {
			any = any || info.Available
			fyne.Do(func() { v.applyInfo(info) })
		}
		if any {
			fyne.Do(func() { v.updateAllBtn.Show() })
		}
	}()
}

// newRow builds the "SMP server | status dot | Update" line followed by
// the description and version line at full width (keeps the description
// from wrapping at word-width and stretching the row).
func (v *updatesView) newRow(k core.Kind) fyne.CanvasObject {
	def := core.Def(k)
	r := &updateRow{}
	r.name = widget.NewLabel(def.Name)
	r.name.TextStyle = fyne.TextStyle{Bold: true}
	r.desc = widget.NewLabel(def.Description)
	r.desc.TextStyle = fyne.TextStyle{Italic: true}
	r.desc.Wrapping = fyne.TextWrapWord

	r.dot = newStatusDot()
	r.dot.set("checking…", theme.ColorNameWarning)
	r.ver = widget.NewLabel("You have: -")
	r.ver.Wrapping = fyne.TextWrapWord

	r.btn = widget.NewButtonWithIcon("Update", theme.DownloadIcon(), func() { v.doUpdate(k) })
	r.btn.Importance = widget.HighImportance
	r.btn.Hide()

	// VBox around the dot keeps the Border from stretching it vertically.
	head := container.NewBorder(nil, nil, r.name, r.btn, container.NewVBox(r.dot.object()))
	r.obj = container.NewVBox(head, r.desc, r.ver)
	v.rows[k] = r
	return container.NewVBox(widget.NewSeparator(), r.obj)
}

// applyInfo fills one row with the check result.
func (v *updatesView) applyInfo(info core.UpdateInfo) {
	r := v.rows[info.Kind]
	if r == nil {
		return
	}
	switch {
	case info.Err != "":
		r.dot.set("check failed", theme.ColorNameError)
		r.ver.SetText("Could not reach GitHub: " + info.Err)
	case info.Available:
		r.dot.set("update available", theme.ColorNameWarning)
		r.ver.SetText(fmt.Sprintf("You have: %s  →  Latest: %s", orUnknown(info.Current), info.Latest))
		r.btn.SetText("Update to " + info.Latest)
		r.btn.Show()
	default:
		r.dot.set("up to date", theme.ColorNameSuccess)
		r.ver.SetText(fmt.Sprintf("You have: %s (latest)", orUnknown(info.Latest)))
		r.btn.Hide()
	}
}

// updateAll updates every component with an available update, one after
// the other, showing progress in a dialog.
func (v *updatesView) updateAll() {
	if v.busy {
		return
	}
	v.busy = true
	fyne.Do(func() { v.updateAllBtn.Disable() })
	d := busyDialog(v.c.win, "Updating everything",
		"Downloading and installing newer versions, then restarting each component…", func() {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
			defer cancel()
			n, problems := v.c.eng.UpdateAll(ctx)
			fyne.Do(func() {
				v.updateAllBtn.Enable()
				switch {
				case len(problems) > 0:
					showError(v.c.win, fmt.Errorf("updated %d component(s).\n\n%s", n, strings.Join(problems, "\n")))
				case n == 0:
					dialog.ShowInformation("No updates", "Everything is already up to date.", v.c.win)
				default:
					dialog.ShowInformation("Updates installed",
						fmt.Sprintf("Updated %d component(s) - you are on the latest versions now.", n), v.c.win)
				}
			})
			v.checkUpdates()
		})
	d.Show()
}

// doUpdate stops the component, installs the new binary and restarts it
// (the engine handles the relay's special start arguments).
func (v *updatesView) doUpdate(k core.Kind) {
	r := v.rows[k]
	if r != nil {
		fyne.Do(func() {
			r.btn.Disable()
			r.dot.set("updating…", theme.ColorNameWarning)
			r.ver.SetText("Downloading the new version and restarting…")
		})
	}
	d := busyDialog(v.c.win, "Updating "+core.Def(k).Name,
		"Downloading the new version and restarting the component…", func() {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
			defer cancel()
			tag, err := v.c.eng.UpdateComponent(ctx, k)
			if err != nil {
				showError(v.c.win, err)
			}
			fyne.Do(func() {
				if r == nil {
					return
				}
				if err != nil {
					r.dot.set("update failed", theme.ColorNameError)
					r.ver.SetText("The update failed - see the message.")
					r.btn.Enable()
					return
				}
				r.dot.set("updated", theme.ColorNameSuccess)
				r.ver.SetText("Updated to " + orUnknown(tag))
				r.btn.Hide()
				r.btn.Enable()
			})
			// refresh all rows + the Update-all button so their state
			// matches reality again (the just-updated component is now
			// current; other pending updates keep their buttons).
			if err == nil {
				v.checkUpdates()
			}
		})
	d.Show()
}

func orUnknown(s string) string {
	if strings.TrimSpace(s) == "" {
		return "unknown"
	}
	return s
}
