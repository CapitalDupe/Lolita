package ui

// v1.6.43 tests: two operator fixes:
//  1. the XFTP moderation card's File ID field points UP at the files
//     list (that card sits ABOVE the moderation card in the admin
//     panel), not down - the placeholder and the card copy both said
//     "below" before;
//  2. the "How to connect" guide no longer walks PHONES through SOCKS
//     proxy setup - Android/iPhone just install Orbot and press Start;
//     the SOCKS proxy instructions survive only in the PC step
//     (Windows/Linux/macOS), where the desktop app genuinely needs
//     them.

import (
	"strings"
	"testing"

	"sxops/internal/core"
)

// TestHowtoPhonesJustRunOrbotV1643: the phone steps are install Orbot
// + press Start, full stop - no SOCKS host/port to type and no app
// restart dance (Orbot routes the app; nothing to configure).
func TestHowtoPhonesJustRunOrbotV1643(t *testing.T) {
	all := strings.Join(howtoLinesTor(), "\n")

	if !strings.Contains(all, "Orbot") || !strings.Contains(all, "press Start") {
		t.Errorf("phone steps must be: install Orbot, press Start:\n%s", all)
	}
	for _, gone := range []string{"set the SimpleX SOCKS proxy", "restart the app"} {
		if strings.Contains(all, gone) {
			t.Errorf("the phone SOCKS walk-through is back (%q):\n%s", gone, all)
		}
	}
}

// TestHowtoPCsKeepSocksInstructionsV1643: the PC step keeps the SOCKS
// proxy instructions - the desktop apps have no Orbot equivalent and
// must be pointed at a Tor via the proxy setting.
func TestHowtoPCsKeepSocksInstructionsV1643(t *testing.T) {
	all := strings.Join(howtoLinesTor(), "\n")

	for _, want := range []string{"SOCKS proxy", "9150", "9050", "own Tor"} {
		if !strings.Contains(all, want) {
			t.Errorf("PC steps lost %q:\n%s", want, all)
		}
	}
}

// TestXftpBanPointsAboveV1643 pins the direction fix: the placeholder
// and the XFTP moderation copy say the file list is ABOVE (it is - the
// files card is the 4th card, the moderation card the 5th), and no
// "list below" wording survives on the XFTP side.
func TestXftpBanPointsAboveV1643(t *testing.T) {
	if got := xftpBanPlaceholder(); got != "file ID from the list above" {
		t.Errorf("XFTP file ID placeholder = %q, want %q", got, "file ID from the list above")
	}
	c := banCardCopy(core.KindXFTP)
	if !strings.Contains(c, "file list above") {
		t.Errorf("XFTP moderation copy must point at the file list above:\n%s", c)
	}
	if strings.Contains(c, "file list below") {
		t.Errorf("XFTP moderation copy still says the file list is below:\n%s", c)
	}
}
