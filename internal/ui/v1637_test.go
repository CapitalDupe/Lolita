package ui

// v1.6.37 tests: the Stop button that aborts a stalled connect, the
// operator-requested wording change, and the ban dialog staying inside
// the window when its live summary text grows.

import (
        "context"
        "strings"
        "testing"
        "time"

        "fyne.io/fyne/v2"
        "fyne.io/fyne/v2/container"
        "fyne.io/fyne/v2/widget"
)

// TestAbortConnectV1637 pins the Stop-during-connect wiring:
// abortConnect cancels the in-flight context and is a safe no-op when
// nothing is running.
func TestAbortConnectV1637(t *testing.T) {
        v := &torView{stopBtn: widget.NewButton("Stop", nil)}
        v.abortConnect() // nothing in flight: no cancel set, must not panic

        ctx, cancel := context.WithCancel(context.Background())
        v.connectCancel = cancel
        v.abortConnect()
        if ctx.Err() == nil {
                t.Error("abortConnect did not cancel the in-flight connect context")
        }
        if v.stopBtn.Text != "Stopping…" {
                t.Errorf("stop button text = %q, want the immediate \"Stopping…\" feedback", v.stopBtn.Text)
        }
}

// TestSpreadWordingV1637 pins the operator-requested wording of the
// post-bootstrap spread line ("Addresses may take a few minutes to
// spread on the Tor network." - was "New addresses take a few minutes
// to spread through the Tor network.").
func TestSpreadWordingV1637(t *testing.T) {
        got := retryStageText(`PROGRESS=100 SUMMARY="Done"`, true, 3*time.Second)
        if !strings.Contains(got, "Addresses may take a few minutes to spread on the Tor network.") {
                t.Errorf("spread line = %q, want the v1.6.37 wording", got)
        }
        if strings.Contains(got, "New addresses take") {
                t.Errorf("spread line still uses the old wording: %q", got)
        }
}

// walkChecks invokes fn for every check in a dialog tree (through
// scroll containers).
func walkChecks(o fyne.CanvasObject, fn func(*widget.Check)) {
        switch x := o.(type) {
        case *container.Scroll:
                walkChecks(x.Content, fn)
        case *fyne.Container:
                for _, ch := range x.Objects {
                        walkChecks(ch, fn)
                }
        case *widget.Check:
                fn(x)
        }
}

// hasScroll reports whether the tree contains a scroll container.
func hasScroll(o fyne.CanvasObject) bool {
        switch x := o.(type) {
        case *container.Scroll:
                return true
        case *fyne.Container:
                for _, ch := range x.Objects {
                        if hasScroll(ch) {
                                return true
                        }
                }
        }
        return false
}

// TestBanDialogFitsWhenTextGrowsV1637 pins the v1.6.37 fix for "the ban
// dialog not fitting in the window when there's a lot of text": the
// dialog body's minimum is clamped to the canvas on EVERY refresh, not
// decided once at open time. Before the fix, a dialog that fit on open
// grew past the canvas once the restrictions were ticked (the live
// "What happens" summary is the extra text) - Fyne then clamped the
// popup to the full window with the question row and buttons off-screen.
func TestBanDialogFitsWhenTextGrowsV1637(t *testing.T) {
        v, w := newTestAdminView(t)
        // capH becomes 560: the unticked dialog fits at open, the fully
        // ticked one exceeds it - exactly the growth case that broke.
        w.Resize(fyne.NewSize(900, 760))

        v.banIDEntry.SetText("testqueueid123")
        v.banDialog("testqueueid123")
        body := topPopupContent(t, w)

        checks := 0
        walkChecks(body, func(c *widget.Check) {
                if !c.Checked {
                        c.SetChecked(true) // fires OnChanged: the summary grows
                }
                checks++
        })
        if checks != 3 {
                t.Fatalf("expected the user ban dialog's three restrictions, found %d", checks)
        }

        m := body.MinSize()
        cs := w.Canvas().Size()
        if m.Height > cs.Height || m.Width > cs.Width {
                t.Errorf("dialog body %.0fx%.0f outgrows the %.0fx%.0f canvas - the popup would be clamped to the full window instead of centered",
                        m.Width, m.Height, cs.Width, cs.Height)
        }
        if !hasScroll(body) {
                t.Error("the ban dialog body has no scroll container - growth past the cap cannot turn into scrolling")
        }
}
