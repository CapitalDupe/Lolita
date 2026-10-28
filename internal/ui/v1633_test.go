package ui

// v1.6.33: the link ban dialog gained its second option - "Delete the
// queue permanently" - next to "Block the link" (reason, no expiry).
// Nothing is ticked by default, the confirm button follows what is
// ticked (Block / Delete), and the reason row rides the block option.
// This test drives the REAL dialog through the fyne test driver.

import (
        "sort"
        "strings"
        "testing"

        "fyne.io/fyne/v2"
        "fyne.io/fyne/v2/container"
        "fyne.io/fyne/v2/widget"

        "sxops/internal/core"
)

// dialogWidgets collects the live widgets of a ban dialog: its checks
// and buttons by text, the reason row container (the Border container
// holding the "Reason recorded by the server:" label) and the live
// summary label.
type dialogWidgets struct {
        checks    map[string]*widget.Check
        buttons   map[string]*widget.Button
        reasonRow *fyne.Container
        summary   *widget.Label
}

func collectWidgets(o fyne.CanvasObject, dw *dialogWidgets) {
        switch x := o.(type) {
        case *fyne.Container:
                for _, ch := range x.Objects {
                        if lbl, ok := ch.(*widget.Label); ok && strings.HasPrefix(strings.TrimSpace(lbl.Text), "Reason recorded by the server:") {
                                dw.reasonRow = x
                        }
                }
                for _, ch := range x.Objects {
                        collectWidgets(ch, dw)
                }
        case *container.Scroll:
                collectWidgets(x.Content, dw)
        case *widget.Label:
                if strings.HasPrefix(x.Text, "What happens to ") || strings.HasPrefix(x.Text, "Nothing is chosen") {
                        dw.summary = x
                }
        case *widget.Check:
                if dw.checks == nil {
                        dw.checks = map[string]*widget.Check{}
                }
                dw.checks[x.Text] = x
        case *widget.Button:
                if dw.buttons == nil {
                        dw.buttons = map[string]*widget.Button{}
                }
                dw.buttons[x.Text] = x
        }
}

func collectTopDialog(t *testing.T, w fyne.Window) *dialogWidgets {
        t.Helper()
        dw := &dialogWidgets{}
        collectWidgets(topPopupContent(t, w), dw)
        return dw
}

// dialogWidgetKeys renders a collected widget map's keys for test
// messages (the widgets themselves are pointers).
func dialogWidgetKeys[T any](m map[string]T) []string {
        ks := make([]string, 0, len(m))
        for k := range m {
                ks = append(ks, k)
        }
        sort.Strings(ks)
        return ks
}

// TestLinkBanDialogTwoOptionsV1633 drives the two-option link dialog
// through every choice combination and asserts the live summary, the
// confirm button's verb and enabled state, and the reason row riding
// the block option (hidden when only the delete is ticked).
func TestLinkBanDialogTwoOptionsV1633(t *testing.T) {
        v, w := newTestAdminView(t)
        link := "https://smp6.simplex.im/g#lrdvu2d8A1GumSmoKb2krQmtKhWXq-tyGpHuM7aMwsw"
        v.banIDEntry.SetText(link)
        id := core.ParseTargetID(link)
        if id == "" {
                t.Fatalf("the pasted link did not parse: %q", link)
        }
        v.banDialog(id)
        dw := collectTopDialog(t, w)

        block := dw.checks["Block the link"]
        del := dw.checks["Delete the queue permanently"]
        if block == nil || del == nil {
                t.Fatalf("the link dialog must carry both options, checks: %v", dialogWidgetKeys(dw.checks))
        }
        if len(dw.checks) != 2 {
                t.Fatalf("the link dialog must have exactly the two options, checks: %v", dialogWidgetKeys(dw.checks))
        }
        if dw.reasonRow == nil {
                t.Fatal("the link dialog must carry the reason row")
        }
        if dw.summary == nil {
                t.Fatal("the link dialog must carry the live summary label")
        }
        confirm := dw.buttons["Block"]
        if confirm == nil {
                t.Fatalf("the link dialog's confirm button must start as Block, buttons: %v", dialogWidgetKeys(dw.buttons))
        }

        // nothing ticked by default: the disabled confirm, the
        // nothing-chosen note, the reason row hidden
        if block.Checked || del.Checked {
                t.Fatal("the link dialog must open with NOTHING ticked")
        }
        if !strings.HasPrefix(dw.summary.Text, "Nothing is chosen") {
                t.Fatalf("the nothing-ticked summary: %q", dw.summary.Text)
        }
        if !confirm.Disabled() {
                t.Error("the confirm button must be disabled until an option is ticked")
        }
        if dw.reasonRow.Visible() {
                t.Error("the reason row must be hidden while the block is unticked")
        }

        // block only: the reason row appears, the summary lists the
        // block, the confirm is Block and enabled
        block.SetChecked(true)
        if !dw.reasonRow.Visible() {
                t.Error("ticking the block must show the reason row")
        }
        if !strings.Contains(dw.summary.Text, "1. Block the link") || strings.Contains(dw.summary.Text, "2.") {
                t.Fatalf("block-only summary: %q", dw.summary.Text)
        }
        if confirm.Disabled() || confirm.Text != "Block" {
                t.Errorf("block ticked: confirm must be enabled and say Block, got %q disabled=%v", confirm.Text, confirm.Disabled())
        }

        // block + delete: both lines, numbered, confirm still Block
        // (the block runs first)
        del.SetChecked(true)
        if !strings.Contains(dw.summary.Text, "1. Block the link") || !strings.Contains(dw.summary.Text, "2. Delete the queue behind the link") {
                t.Fatalf("block + delete summary: %q", dw.summary.Text)
        }
        if !dw.reasonRow.Visible() {
                t.Error("the reason row must stay visible while the block is ticked")
        }
        if confirm.Text != "Block" {
                t.Errorf("block + delete: confirm must stay Block (the block runs first), got %q", confirm.Text)
        }

        // delete only: the reason row hides again, the summary is the
        // delete line alone, the confirm becomes Delete
        block.SetChecked(false)
        if dw.reasonRow.Visible() {
                t.Error("the delete alone carries no reason - the row must hide")
        }
        if !strings.Contains(dw.summary.Text, "1. Delete the queue behind the link") || strings.Contains(dw.summary.Text, "Block the link") {
                t.Fatalf("delete-only summary: %q", dw.summary.Text)
        }
        if confirm.Text != "Delete" {
                t.Errorf("delete only: the confirm verb must be Delete, got %q", confirm.Text)
        }
        if confirm.Disabled() {
                t.Error("the delete alone is a valid choice - the confirm must be enabled")
        }

        // back to nothing: disabled again
        del.SetChecked(false)
        if !confirm.Disabled() || !strings.HasPrefix(dw.summary.Text, "Nothing is chosen") {
                t.Fatalf("unticking everything must disable the confirm again, summary: %q", dw.summary.Text)
        }
}

// TestLinkBanDialogCopyV1633: the link dialog's copy - the question,
// the delete note naming the queue behind the link - and the card
// copy describing the two-option dialog.
func TestLinkBanDialogCopyV1633(t *testing.T) {
        v, w := newTestAdminView(t)
        link := "simplex:/g#lrdvu2d8A1GumSmoKb2krQmtKhWXq-tyGpHuM7aMwsw?h=smp6.simplex.im"
        v.banIDEntry.SetText(link)
        id := core.ParseTargetID(link)
        if id == "" {
                t.Fatalf("the compact link did not parse: %q", link)
        }
        v.banDialog(id)
        snap := snapshotTopDialog(t, w)
        all := joined(snap.labels)
        if !strings.Contains(all, "Ban this link?") {
                t.Errorf("the link dialog's question must be \"Ban this link?\", labels: %q", snap.labels)
        }
        if !strings.Contains(all, "The queue behind the link and any undelivered messages") {
                t.Errorf("the delete note must name the queue behind the link, labels: %q", snap.labels)
        }
        if !strings.Contains(all, "choose what happens to it") {
                t.Errorf("the link dialog must invite the choice, labels: %q", snap.labels)
        }

        // the card copy describes the two-option dialog and no longer
        // says the delete is not part of it
        card := banCardCopy(core.KindSMP)
        if !strings.Contains(card, "and/or the permanent delete of the queue behind the link, in one go") {
                t.Errorf("the SMP card copy must describe the two-option link dialog")
        }
        if strings.Contains(card, "ONE-option") || strings.Contains(card, "Deleting the queue behind a link is not part of that dialog") {
                t.Errorf("the SMP card copy must not carry the retired one-option wording")
        }
}
