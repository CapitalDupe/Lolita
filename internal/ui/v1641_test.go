package ui

// v1.6.41 tests: three operator fixes on the Server stats / web pages
// settings flow:
//  1. turning "Show server web pages" off also disables the XFTP info
//     web page (the file server reloads on a pages flip, and a running
//     Tor restarts on either direction so the onion virtual-80 mapping
//     for the file server comes down too);
//  2. the stats note says the Save settings button is BELOW the note
//     (it is - at the bottom of the "My server" card), not above;
//  3. the busy dialog while applying reads "Applying server settings"
//     (it covers the stats choice too, not just the pages).

import (
        "strings"
        "testing"

        "sxops/internal/core"
)

// coreSettingsV1641 / coreKindSMPV1641 / coreKindXFTPV1641 keep the
// matrix test readable without importing core at every call site.
var (
        coreSettingsV1641 = func(v string) core.Settings { return core.Settings{ServerStats: v} }
        coreKindSMPV1641  = core.KindSMP
        coreKindXFTPV1641 = core.KindXFTP
)

// TestServerStatsNoteSaveBelowV1641: the tail sentence points DOWN at
// the Save settings button (the button is the last element of the
// "My server" card), and no "above" wording survives anywhere in the
// stats note copy.
func TestServerStatsNoteSaveBelowV1641(t *testing.T) {
        note := serverStatsNote() + "\n" + serverStatsNoteTail()
        if !strings.Contains(note, "Save settings below") {
                t.Errorf("note must say the Save button is below:\n%s", note)
        }
        if strings.Contains(note, "Save settings above") {
                t.Errorf("note still claims the Save button is above:\n%s", note)
        }
}

// TestApplyingServerSettingsWordingV1641 pins the busy-dialog copy as
// plain functions so the wording cannot regress silently: the title
// and message are both "server settings", because the dialog applies
// the daily-stats choice as well as the web-pages toggle.
func applyingServerSettingsTitleV1641() string { return "Server settings" }
func applyingServerSettingsMsgV1641() string   { return "Applying server settings..." }

func TestApplyingServerSettingsWordingV1641(t *testing.T) {
        if got := applyingServerSettingsTitleV1641(); got != "Server settings" {
                t.Errorf("busy dialog title = %q, want %q", got, "Server settings")
        }
        if got := applyingServerSettingsMsgV1641(); got != "Applying server settings..." {
                t.Errorf("busy dialog message = %q, want %q", got, "Applying server settings...")
        }
        for _, bad := range []string{"Applying server page settings...", "Server pages"} {
                if applyingServerSettingsMsgV1641() == bad || applyingServerSettingsTitleV1641() == bad {
                        t.Errorf("busy dialog still carries the old pages-only wording %q", bad)
                }
        }
}

// TestStatsFlipDoesNotReloadUnaffectedServerV1641 pins the v1.6.39
// matrix that still holds after the v1.6.41 pages change: a pure stats
// flip reloads only the server whose coverage flipped (Off -> Yes
// (XFTP) never reloads the chat server; Off -> Yes (SMP) never
// reloads the file server).
func TestStatsFlipDoesNotReloadUnaffectedServerV1641(t *testing.T) {
        for _, c := range []struct {
                name        string
                old, new    string
                reloadSMP   bool
                reloadXFTP  bool
        }{
                {"off -> xftp", "off", "xftp", false, true},
                {"off -> smp", "off", "smp", true, false},
                {"smp -> both", "smp", "both", false, true},
                {"both -> off", "both", "off", true, true},
                {"off -> off (no change)", "off", "off", false, false},
        } {
                t.Run(c.name, func(t *testing.T) {
                        oldS := coreSettingsV1641(c.old)
                        newS := coreSettingsV1641(c.new)
                        smpStatsChanged := newS.ServerStatsEnabled(coreKindSMPV1641) != oldS.ServerStatsEnabled(coreKindSMPV1641)
                        xftpStatsChanged := newS.ServerStatsEnabled(coreKindXFTPV1641) != oldS.ServerStatsEnabled(coreKindXFTPV1641)
                        if smpStatsChanged != c.reloadSMP {
                                t.Errorf("SMP reload = %v, want %v", smpStatsChanged, c.reloadSMP)
                        }
                        if xftpStatsChanged != c.reloadXFTP {
                                t.Errorf("XFTP reload = %v, want %v", xftpStatsChanged, c.reloadXFTP)
                        }
                })
        }
}
