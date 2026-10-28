package ui

import (
        "strings"
        "testing"
        "time"

        "fyne.io/fyne/v2"
        "fyne.io/fyne/v2/container"
        "fyne.io/fyne/v2/test"
        "fyne.io/fyne/v2/widget"
)

// TestRetryStageText pins the v1.5.5 fix for "Tor stuck in connecting when
// started from the Tor network panel": once the bootstrap phase reports
// 100% the live status must switch to the connected/publishing stage - it
// used to say "Connecting to the Tor network…" for the whole flow, even
// with "100% - Done" underneath, for minutes while the onion addresses
// were verified.
func TestRetryStageText(t *testing.T) {
        bootstrapped := "PROGRESS=100 SUMMARY=\"Done\""

        got := retryStageText(bootstrapped, true, 30*time.Second)
        if strings.Contains(got, "Connecting to the Tor network") {
                t.Errorf("bootstrapped stage still says connecting: %q", got)
        }
        if !strings.Contains(got, "Tor is connected") {
                t.Errorf("bootstrapped stage = %q, want the connected/publishing text", got)
        }

        got = retryStageText("PROGRESS=45 SUMMARY=\"Connecting to relay\"", true, 12*time.Second)
        if !strings.Contains(got, "Connecting to the Tor network") || !strings.Contains(got, "45%") {
                t.Errorf("mid-bootstrap stage = %q, want the connecting text with progress", got)
        }

        got = retryStageText("", false, 7*time.Second)
        if !strings.Contains(got, "Starting Tor") || !strings.Contains(got, "7 seconds") {
                t.Errorf("starting stage = %q, want the starting text with elapsed seconds", got)
        }

        // a control port that answers without a parseable phase must not be
        // mistaken for "connected" either
        got = retryStageText("", true, 5*time.Second)
        if !strings.Contains(got, "Connecting to the Tor network") {
                t.Errorf("unparseable phase = %q, want the connecting fallback", got)
        }
}

// TestApplyBridgesLabel pins the v1.6.34 rule: the bridges button says
// "Apply and restart Tor" only when Tor is running - with Tor off it
// only saves the lines ("Apply bridges"), because applying bridges must
// not start Tor.
func TestApplyBridgesLabel(t *testing.T) {
        if got := applyBridgesLabel(true); got != "Apply and restart Tor" {
                t.Errorf("applyBridgesLabel(true) = %q, want %q", got, "Apply and restart Tor")
        }
        if got := applyBridgesLabel(false); got != "Apply bridges" {
                t.Errorf("applyBridgesLabel(false) = %q, want %q", got, "Apply bridges")
        }
        if strings.Contains(applyBridgesLabel(false), "restart") {
                t.Errorf("applyBridgesLabel(false) must not promise a restart: %q", applyBridgesLabel(false))
        }
}

// TestTorLogPaneSizingV1636 pins the v1.6.36 fix for "the Tor log text
// box is very small": the log entry is a real 14-row pane placed
// directly in the card. In v1.6.35 and earlier it sat inside
// container.NewVScroll, and a VScroll's minimum height is its own
// hard-coded 32px chrome minimum regardless of its content - so the
// log card collapsed to a single line of text no matter how much log
// was fetched.
func TestTorLogPaneSizingV1636(t *testing.T) {
        a := test.NewApp()
        t.Cleanup(a.Quit)

        newLogEntry := func(rows int) *widget.Entry {
                e := widget.NewMultiLineEntry()
                e.TextStyle = fyne.TextStyle{Monospace: true}
                e.Wrapping = fyne.TextWrapBreak // same as newTorView
                e.SetMinRowsVisible(rows)
                e.Disable()
                e.SetText(strings.Repeat(
                        "Oct 26 12:00:00.000 [notice] Bootstrapped 25% (tag): Asking for networkstatus consensus\n", 200))
                return e
        }

        // the v1.6.36 construction: a stable multi-row pane whose MinSize
        // comes from the row count, never from the 200 lines of content
        pane := newLogEntry(14)
        if h := pane.MinSize().Height; h < 200 {
                t.Errorf("14-row log pane min height = %.0fpx, want a real pane (>= 200px)", h)
        }

        // the pre-v1.6.36 construction: the same entry wrapped in a VScroll
        // has the ~32px chrome minimum no matter how many rows were requested
        collapsed := container.NewVScroll(newLogEntry(14))
        if h := collapsed.MinSize().Height; h > 64 {
                t.Errorf("VScroll-wrapped log pane min height = %.0fpx, expected the ~32px chrome minimum (Fyne layout changed?)", h)
        }
        if collapsed.MinSize().Height >= pane.MinSize().Height {
                t.Errorf("the wrapped pane (%.0fpx) must collapse far below the fixed pane (%.0fpx)",
                        collapsed.MinSize().Height, pane.MinSize().Height)
        }
}
