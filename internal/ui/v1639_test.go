package ui

// v1.6.39 tests: the daily-statistics select (Off / Yes (SMP) /
// Yes (XFTP) / Both (SMP + XFTP), default Off) and the XFTP
// registration-gate wording that now talks about FILE UPLOADS (the
// official [AUTH] new_files method).

import (
        "strings"
        "testing"

        "fyne.io/fyne/v2/widget"

        "sxops/internal/core"
)

// TestServerStatsChoicesV1639: every choice round-trips through
// serverStatsValueOf/serverStatsLabel, and unknown or empty persisted
// values (old states) render as "Off".
func TestServerStatsChoicesV1639(t *testing.T) {
        want := []string{"Off", "Yes (SMP)", "Yes (XFTP)", "Both (SMP + XFTP)"}
        if len(serverStatsChoices) != len(want) {
                t.Fatalf("serverStatsChoices = %v, want %v", serverStatsChoices, want)
        }
        for i, c := range want {
                if serverStatsChoices[i] != c {
                        t.Errorf("choice %d = %q, want %q", i, serverStatsChoices[i], c)
                }
                v := serverStatsValueOf[c]
                if l := serverStatsLabel(v); l != c {
                        t.Errorf("round trip %q -> %q -> %q", c, v, l)
                }
        }
        for _, unknown := range []string{"", "nope", "SMP"} {
                if l := serverStatsLabel(unknown); l != "Off" {
                        t.Errorf("serverStatsLabel(%q) = %q, want Off (default)", unknown, l)
                }
        }
}

// TestServerStatsSelectDefaultsToOffV1639: an old state (no stats
// value) renders the select at "Off", and a state carrying "both"
// renders at "Both (SMP + XFTP)" - the same wiring newSettingsView
// and refresh use.
func TestServerStatsSelectDefaultsToOffV1639(t *testing.T) {
        for _, c := range []struct{ value, want string }{
                {"", "Off"},
                {"off", "Off"},
                {"smp", "Yes (SMP)"},
                {"xftp", "Yes (XFTP)"},
                {"both", "Both (SMP + XFTP)"},
        } {
                sel := widget.NewSelect(serverStatsChoices, nil)
                sel.SetSelected(serverStatsLabel(c.value))
                if sel.Selected != c.want {
                        t.Errorf("value %q rendered as %q, want %q", c.value, sel.Selected, c.want)
                }
        }
}

// TestServerStatsNoteLocalOnlyV1639 pins the corrected settings copy:
// the daily totals stay in LOCAL log files and the public info page
// shows only a "stats enabled" line - the first draft wrongly claimed
// the numbers are shown on the public page (upstream SMPWeb.hs renders
// only yesNo statsEnabled; confirmed on the official servers' pages).
// v1.6.40 split the note around the two log-file path lines, so the
// check runs over head + tail joined (the v1640 test pins the paths).
func TestServerStatsNoteLocalOnlyV1639(t *testing.T) {
        note := serverStatsNote() + "\n" + serverStatsNoteTail()
        if !strings.Contains(note, "LOCAL log") {
                t.Errorf("note lost the local-only wording:\n%s", note)
        }
        if !strings.Contains(note, `"stats enabled"`) {
                t.Errorf("note lost the public-page indicator wording:\n%s", note)
        }
        if strings.Contains(note, "shown on the PUBLIC info page") {
                t.Errorf("note still claims the numbers are public:\n%s", note)
        }
        // v1.6.40: the caption stays "Collect daily server statistics:" -
        // the operator tried "Server stats (Log):" and reverted it; the
        // v1640 test pins that revert.
        if l := serverStatsSettingLabel(); l != "Collect daily server statistics:" {
                t.Errorf("setting label = %q, want the collect wording", l)
        }
}

// TestGateLineXFTPUploadsV1639: the XFTP gate summary speaks of file
// uploads (not "registrations"), keeps the new_files key visible, and
// the SMP line is unchanged.
func TestGateLineXFTPUploadsV1639(t *testing.T) {
        s := gateLine(core.KindXFTP, true)
        if !strings.Contains(s, "new_files = off") || !strings.Contains(s, "NEW file uploads") {
                t.Errorf("XFTP blocked line lost the upload wording: %q", s)
        }
        if strings.Contains(s, "registrations") {
                t.Errorf("XFTP blocked line still says registrations: %q", s)
        }
        s = gateLine(core.KindXFTP, false)
        if !strings.Contains(s, "new_files = on") || !strings.Contains(s, "file uploads are allowed") {
                t.Errorf("XFTP allowed line: %q", s)
        }
        s = gateLine(core.KindSMP, true)
        if !strings.Contains(s, "new_queues = off") || !strings.Contains(s, "NEW registrations") {
                t.Errorf("SMP blocked line changed unexpectedly: %q", s)
        }
}

// TestSetGateUIXFTPUploadsV1639 pins the gate card copy on the file
// server tab: the button offers to refuse/allow FILE UPLOADS, and the
// SMP tab keeps the registrations wording.
func TestSetGateUIXFTPUploadsV1639(t *testing.T) {
        v, _ := newTestAdminView(t)
        v.gateLbl = widget.NewLabel("")
        v.gateBtn = widget.NewButton("", nil)
        v.gateCard = widget.NewLabel("gate card")
        v.selected = core.KindXFTP

        v.setGateUI(true, true)
        if v.gateBtn.Text != "Allow new file uploads" {
                t.Errorf("blocked XFTP button = %q", v.gateBtn.Text)
        }
        if !strings.Contains(v.gateLbl.Text, "NEW UPLOADS REFUSED") || !strings.Contains(v.gateLbl.Text, "new_files = off") {
                t.Errorf("blocked XFTP label: %q", v.gateLbl.Text)
        }

        v.setGateUI(false, true)
        if v.gateBtn.Text != "Refuse new file uploads" {
                t.Errorf("allowed XFTP button = %q", v.gateBtn.Text)
        }
        if !strings.Contains(v.gateLbl.Text, "new_files = on") {
                t.Errorf("allowed XFTP label: %q", v.gateLbl.Text)
        }

        // SMP tab keeps the registrations wording.
        v.selected = core.KindSMP
        v.setGateUI(true, true)
        if v.gateBtn.Text != "Allow new registrations again" {
                t.Errorf("blocked SMP button = %q", v.gateBtn.Text)
        }
        if !strings.Contains(v.gateLbl.Text, "new_queues = off") {
                t.Errorf("blocked SMP label: %q", v.gateLbl.Text)
        }
}
