package ui

import (
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
	qrcode "github.com/skip2/go-qrcode"
)

// statusDot is a small colored dot + bold text chip used for statuses.
type statusDot struct {
	rect *canvas.Rectangle
	lbl  *widget.Label
	obj  fyne.CanvasObject
}

func newStatusDot() *statusDot {
	d := &statusDot{
		rect: canvas.NewRectangle(theme.Color(theme.ColorNameSuccess)),
		lbl:  widget.NewLabel("…"),
	}
	d.rect.SetMinSize(fyne.NewSize(12, 12))
	d.lbl.TextStyle = fyne.TextStyle{Bold: true}
	d.obj = container.NewHBox(container.NewPadded(d.rect), d.lbl)
	return d
}

func (d *statusDot) set(text string, color fyne.ThemeColorName) {
	d.rect.FillColor = theme.Color(color)
	d.rect.Refresh()
	d.lbl.SetText(text)
}

func (d *statusDot) object() fyne.CanvasObject { return d.obj }

// copyButton returns a button that copies text to the clipboard and
// briefly confirms. The text is resolved through fn at click time so the
// button can live inside a persistent, updated-in-place row.
func copyButton(win fyne.Window, fn func() string) *widget.Button {
	var b *widget.Button
	b = widget.NewButtonWithIcon("Copy", theme.ContentCopyIcon(), func() {
		win.Clipboard().SetContent(fn())
		b.SetText("Copied!")
		time.AfterFunc(1500*time.Millisecond, func() {
			fyne.Do(func() { b.SetText("Copy") })
		})
	})
	b.Importance = widget.LowImportance
	return b
}

// monoLabel is a selectable monospace label.
func monoLabel(text string) *widget.Entry {
	e := widget.NewMultiLineEntry()
	e.TextStyle = fyne.TextStyle{Monospace: true}
	e.Wrapping = fyne.TextWrapBreak
	// 2 visible rows: enough for a wrapped smp:// address without the
	// empty third line the default 3-row minimum leaves under it
	e.SetMinRowsVisible(2)
	e.Disable()
	e.SetText(text) // after Disable: SetText keeps the widget state consistent
	return e
}

// card wraps content in a padded box with a title.
func card(title string, subtitle string, content fyne.CanvasObject) fyne.CanvasObject {
	head := widget.NewLabel(title)
	head.TextStyle = fyne.TextStyle{Bold: true}
	var sub fyne.CanvasObject
	if subtitle != "" {
		l := widget.NewLabel(subtitle)
		l.Wrapping = fyne.TextWrapWord
		sub = l
	} else {
		sub = widget.NewLabel("")
		sub.Hide()
	}
	return container.NewVBox(
		container.NewVBox(head, sub),
		widget.NewSeparator(),
		container.NewPadded(content),
	)
}

// showQR opens a dialog with a QR code for the given text.
func showQR(win fyne.Window, title, text string) {
	q, err := qrcode.New(text, qrcode.Medium)
	if err != nil {
		dialog.NewError(err, win).Show()
		return
	}
	// 320px keeps the whole dialog (QR + label + copyable address +
	// Close button) inside a 700px-tall window - a 420px image pushed
	// the Close button below the screen edge, leaving the dialog
	// unclosable by mouse on small windows
	img := canvas.NewImageFromImage(q.Image(320))
	img.FillMode = canvas.ImageFillContain
	// canvas.Image reports a zero MinSize unless one is set explicitly,
	// which would collapse the QR to nothing inside the dialog VBox.
	img.SetMinSize(fyne.NewSize(320, 320))
	// selectable copyable text below the QR (multi-line: wrapping a
	// single-line Entry is invalid and logged an error by Fyne)
	e := widget.NewMultiLineEntry()
	e.TextStyle = fyne.TextStyle{Monospace: true}
	e.Wrapping = fyne.TextWrapBreak
	e.Disable()
	e.SetText(text)
	d := dialog.NewCustom(title, "Close", container.NewVBox(
		img,
		widget.NewLabel("Scan with the SimpleX app, or copy the address below:"),
		e,
	), win)
	d.Resize(fyne.NewSize(500, 600))
	d.Show()
}

// busyDialog shows a non-dismissable progress dialog while fn runs in a
// goroutine; on completion the dialog closes and done (if set) runs on
// the main thread.
func busyDialog(win fyne.Window, title, msg string, fn func()) *dialog.CustomDialog {
	inf := widget.NewProgressBarInfinite()
	inf.Start()
	box := container.NewVBox(
		widget.NewLabel(msg),
		inf,
	)
	d := dialog.NewCustomWithoutButtons(title, box, win)
	d.SetOnClosed(func() {})
	go func() {
		fn()
		fyne.Do(func() { d.Hide() })
	}()
	return d
}

// showError shows an error dialog from any goroutine.
func showError(win fyne.Window, err error) {
	fyne.Do(func() {
		dialog.NewError(err, win).Show()
	})
}
