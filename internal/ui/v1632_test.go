package ui

// v1.6.32 split the link ban into its own dialog; v1.6.33 gave it
// its second option (Delete queue). These tests drive the REAL
// dialogs through the fyne test driver and assert what is - and is
// not - on them.

import (
        "os"
        "reflect"
        "strings"
        "testing"

        "fyne.io/fyne/v2"
        "fyne.io/fyne/v2/container"
        "fyne.io/fyne/v2/test"
        "fyne.io/fyne/v2/widget"

        "sxops/internal/core"
)

// dialogSnapshot collects every label, check, select and button text
// from a dialog body tree.
type dialogSnapshot struct {
        labels  []string
        checks  []string
        selects [][]string
        buttons []string
}

func snapshotTree(o fyne.CanvasObject, snap *dialogSnapshot) {
        switch x := o.(type) {
        case *fyne.Container:
                for _, ch := range x.Objects {
                        snapshotTree(ch, snap)
                }
        case *container.Scroll:
                // capToWindow wraps tall dialog bodies in a VScroll
                // (fyne v2.5+: container.Scroll, not the widget)
                snapshotTree(x.Content, snap)
        case *widget.Label:
                snap.labels = append(snap.labels, x.Text)
        case *widget.Check:
                snap.checks = append(snap.checks, x.Text)
        case *widget.Select:
                snap.selects = append(snap.selects, x.Options)
        case *widget.Button:
                snap.buttons = append(snap.buttons, x.Text)
        }
}

// topPopupContent returns the dialog body of the topmost overlay
// popup. The canvas overlay list carries an internal OverlayContainer
// whose Content is the *widget.PopUp itself (fyne v2.8 popup.go Show:
// NewOverlayContainer(p.super(), ...)), and the PopUp's own Content
// field is the body the dialog was built with - so the reflection
// walks two hops. checkCentered does the same first hop.
func topPopupContent(t *testing.T, w fyne.Window) fyne.CanvasObject {
        t.Helper()
        ols := w.Canvas().Overlays().List()
        if len(ols) == 0 {
                t.Fatalf("no dialog was shown")
        }
        ov := reflect.ValueOf(ols[len(ols)-1])
        if ov.Kind() == reflect.Ptr {
                ov = ov.Elem()
        }
        pop := ov.FieldByName("Content")
        if !pop.IsValid() || pop.IsZero() {
                t.Fatalf("the overlay carries no popup")
        }
        if pop.Kind() == reflect.Interface {
                pop = pop.Elem()
        }
        if pop.Kind() == reflect.Ptr {
                pop = pop.Elem()
        }
        body := pop.FieldByName("Content")
        if !body.IsValid() || body.IsZero() {
                t.Fatalf("the popup carries no dialog body")
        }
        c, ok := body.Interface().(fyne.CanvasObject)
        if !ok {
                t.Fatalf("the popup Content is not a canvas object")
        }
        return c
}

func snapshotTopDialog(t *testing.T, w fyne.Window) dialogSnapshot {
        t.Helper()
        var snap dialogSnapshot
        snapshotTree(topPopupContent(t, w), &snap)
        return snap
}

func joined(ss []string) string { return strings.Join(ss, "\n") }

// newTestAdminView builds a minimal adminView around a fresh test
// window, the way TestBanDialogStaysCentered does.
func newTestAdminView(t *testing.T) (*adminView, fyne.Window) {
        t.Helper()
        a := test.NewApp()
        t.Cleanup(a.Quit)
        w := a.NewWindow("")
        w.Resize(fyne.NewSize(900, 600))
        c := &console{app: a, win: w, glog: &guiLog{}}
        v := &adminView{c: c, selected: core.KindSMP}
        v.banIDEntry = widget.NewEntry()
        return v, w
}

// TestLinkBanDialogSingleOptionV1632 (updated v1.6.33): pasting a
// link (either spelling) opens the LINK dialog - "Block the link"
// with the reason select and "Delete the queue permanently", still NO
// expiry row anywhere, nothing ticked by default.
func TestLinkBanDialogSingleOptionV1632(t *testing.T) {
        for _, tc := range []struct {
                name string
                link string
        }{
                {"https short group link", "https://smp6.simplex.im/g#lrdvu2d8A1GumSmoKb2krQmtKhWXq-tyGpHuM7aMwsw"},
                {"compact simplex:/ short link", "simplex:/g#lrdvu2d8A1GumSmoKb2krQmtKhWXq-tyGpHuM7aMwsw?h=smp6.simplex.im"},
        } {
                t.Run(tc.name, func(t *testing.T) {
                        v, w := newTestAdminView(t)
                        v.banIDEntry.SetText(tc.link)
                        id := core.ParseTargetID(tc.link)
                        if id == "" {
                                t.Fatalf("the pasted link did not parse: %q", tc.link)
                        }
                        v.banDialog(id)
                        snap := snapshotTopDialog(t, w)

                        // exactly the two options (v1.6.33), nothing
                        // ticked by default
                        if len(snap.checks) != 2 || joined(snap.checks) != "Block the link\nDelete the queue permanently" {
                                t.Errorf("the link dialog must be exactly [Block the link / Delete the queue permanently], found %q", snap.checks)
                        }
                        all := joined(snap.labels)
                        if !strings.Contains(all, "Reason recorded by the server:") {
                                t.Errorf("the link dialog must carry the reason row, labels: %q", snap.labels)
                        }
                        if !strings.Contains(all, "no") || !strings.Contains(all, "expiry") {
                                t.Errorf("the link dialog must say there is no expiry, labels: %q", snap.labels)
                        }
                        if !strings.Contains(all, "Nothing is chosen") {
                                t.Errorf("the link dialog opens with nothing ticked, labels: %q", snap.labels)
                        }
                        for _, forbidden := range []string{
                                "For how long", "Block the queue",
                                "Block link creation", "Choose what happens",
                        } {
                                if strings.Contains(all, forbidden) {
                                        t.Errorf("the link dialog must not mention %q, labels: %q", forbidden, snap.labels)
                                }
                        }
                        if len(snap.selects) != 1 || joined(snap.selects[0]) != "Spam\nInappropriate content" {
                                t.Errorf("the link dialog must have exactly the reason select (Spam / Inappropriate content), got %q", snap.selects)
                        }
                        if !strings.Contains(joined(snap.buttons), "Block") {
                                t.Errorf("the link dialog confirm button must start as Block, buttons: %q", snap.buttons)
                        }
                })
        }
}

// TestLinkRowBanOpensLinkDialogV1632: the queue list's Ban button on a
// row marked "link" pastes the row's QUEUE ID - v1.6.32 classifies that
// as a link ban, so the link dialog opens (this was the "dialog
// still wrong" root cause: link rows used to open the USER dialog with
// its expiry field). An ordinary queue keeps the full restrictions
// dialog, and a blocked queue is never a link shape.
func TestLinkRowBanOpensLinkDialogV1632(t *testing.T) {
        const (
                linkQID = "LcJUMfVhwD8yxjAiSaDzzGF3-kLG4Uh0Fl_ZIjrRwjI"
                linkLID = "Zfq4GTzixVwEUBkNxiNs8WxBoUMJKaf5"
                userQID = "q1AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"
                blkQID  = "b1AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"
        )
        v, w := newTestAdminView(t)
        v.lastQueues = []core.QueueInfo{
                {ID: linkQID, LinkID: linkLID, LinkLive: true},
                {ID: userQID},
                {ID: blkQID, LinkID: "blk1AAAAAAAA", LinkLive: true, Blocked: true},
        }

        // the link-marked row's own queue ID -> the link dialog (two
        // options since v1.6.33)
        v.banIDEntry.SetText(linkQID)
        if got := v.banTargetShape(linkQID); got != core.ShapeLink {
                t.Fatalf("the queue ID of a link-marked row must classify as a link ban, got %v", got)
        }
        v.banDialog(linkQID)
        snap := snapshotTopDialog(t, w)
        if len(snap.checks) != 2 || !strings.Contains(joined(snap.checks), "Block the link") || !strings.Contains(joined(snap.checks), "Delete the queue permanently") {
                t.Fatalf("the link-marked row's Ban button must open the link dialog (Block the link + Delete the queue permanently), got checks %q labels %q", snap.checks, snap.labels)
        }

        // the row's link ID still classifies as a link ban too
        v.banIDEntry.SetText(linkLID)
        if got := v.banTargetShape(linkLID); got != core.ShapeLink {
                t.Errorf("a bare live link ID must classify as a link ban, got %v", got)
        }

        // a blocked queue (its link is dead, the row shows "banned") is
        // never a link shape
        v.banIDEntry.SetText(blkQID)
        if got := v.banTargetShape(blkQID); got != core.ShapeBareID {
                t.Errorf("a blocked queue must not classify as a link ban, got %v", got)
        }

        // an ordinary user queue keeps the full restrictions dialog
        v.banIDEntry.SetText(userQID)
        if got := v.banTargetShape(userQID); got != core.ShapeBareID {
                t.Fatalf("an ordinary queue must classify as a user ban, got %v", got)
        }
        v.banDialog(userQID)
        snap = snapshotTopDialog(t, w)
        if len(snap.checks) == 0 || !strings.Contains(joined(snap.checks), "Block the queue") {
                t.Fatalf("an ordinary queue must open the user restrictions dialog, got checks %q labels %q buttons %q", snap.checks, snap.labels, snap.buttons)
        }
        if !strings.Contains(joined(snap.labels), "For how long") {
                t.Errorf("the user dialog keeps its expiry select on Block link creation, labels: %q", snap.labels)
        }
        if !strings.Contains(joined(snap.labels), "Nothing is chosen") {
                t.Errorf("the user dialog keeps its nothing-ticked default note, labels: %q", snap.labels)
        }
}

// TestLinkBanChoicesV1632 (updated v1.6.33): the link dialog's choice
// set is the link block and the delete - the chosen reason rides the
// block, no notice ever - and banReasonToSend passes the chosen reason
// through only when the block is ticked.
func TestLinkBanChoicesV1632(t *testing.T) {
        ch := banChoices{link: true, reason: core.BanReasonContent}
        if !ch.valid() || ch.withNotice() {
                t.Fatal("the link block choice must be valid without a notice")
        }
        if got := banReasonToSend(core.KindSMP, core.ShapeLink, ch); got != core.BanReasonContent {
                t.Fatalf("the link ban must send the chosen reason, got %q", got)
        }
        ch.reason = core.BanReasonSpam
        if got := banReasonToSend(core.KindSMP, core.ShapeLink, ch); got != core.BanReasonSpam {
                t.Fatalf("the link ban must send the chosen reason, got %q", got)
        }
        if !reasonVisible(core.KindSMP, core.ShapeLink, banChoices{link: true}) {
                t.Fatal("the reason row must be visible on the link dialog")
        }
        // v1.6.33: the delete alone carries no reason (the delete sends
        // none - it is the block that records one)
        if reasonVisible(core.KindSMP, core.ShapeLink, banChoices{del: true}) {
                t.Fatal("the link delete alone must not show the reason row")
        }
        // the notice/expiry machinery stays user-ban only
        if reasonVisible(core.KindSMP, core.ShapeBareID, banChoices{block: true}) {
                t.Fatal("user bans keep no reason row")
        }
}

// TestCloseStopsServersBeforeExitV1632 pins the v1.6.32 close-window
// fix: "Stop servers" must WAIT for the stop before the app exits. The
// old flow fired "go c.eng.StopAll(true)" and closed the window at
// once - the goroutine died with the process, and the sequential stops
// left everything after SMP (XFTP, NTF, Tor) running.
func TestCloseStopsServersBeforeExitV1632(t *testing.T) {
        src, err := os.ReadFile("app.go")
        if err != nil {
                t.Skip("source not readable from test dir")
        }
        s := string(src)
        if strings.Contains(s, "go c.eng.StopAll") {
                t.Error("the close flow must not fire StopAll in a fire-and-forget goroutine - it died with the app process and left XFTP running")
        }
        for _, want := range []string{
                "c.stopping = true",                    // the re-entry guard
                `busyDialog(c.win, "Stopping servers"`, // the stop runs under a progress dialog
                `close(done)`,                          // the stop signals completion
                "time.After(75 * time.Second)",         // the watchdog closes anyway if anything hangs
        } {
                if !strings.Contains(s, want) {
                        t.Errorf("app.go must contain %q (the stop-before-close flow)", want)
                }
        }
        // the engine side: StopAll stops the servers in parallel so the
        // close is as fast as the slowest single server
        esrc, err := os.ReadFile("../core/engine.go")
        if err != nil {
                t.Skip("engine source not readable")
        }
        e := string(esrc)
        if !strings.Contains(e, "wg.Wait()") {
                t.Error("Engine.StopAll must wait for the parallel server stops (wg.Wait)")
        }
}
