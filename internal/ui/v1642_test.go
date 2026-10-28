package ui

// v1.6.42 tests: four operator fixes:
//  1. the "How to connect" card no longer tells phones to tick a
//     "Use Tor" switch that does not exist in the SimpleX apps -
//     Android/iPhone connect through Orbot's SOCKS proxy instead;
//  2. the card (and READMEs) now also cover PCs: Windows, Linux and
//     macOS machines need their own Tor (Tor Browser port 9150, or a
//     system Tor at 9050, or sxops' own Tor on this machine);
//  3. the log panes (Logs view + Tor panel) stick to the bottom when
//     new lines arrive instead of jumping back to the top;
//  4. opening the Fyne file dialog no longer logs the "this computer
//     does not define a VIDEOS folder" error on installs without
//     complete XDG user dirs. (v1.6.42 did this by creating the
//     missing $HOME folders; v1.6.46 retired that approach - the
//     operator wanted no folder side effects - and muffles the log
//     line instead, pinned by v1646_test.go.)

import (
        "strings"
        "testing"
)

// TestHowtoPhonesUseOrbotV1642 pins the phone path: Orbot + SOCKS
// 127.0.0.1:9050, and the retired "turn on Use Tor" instruction must
// not come back (the SimpleX apps have no such switch).
func TestHowtoPhonesUseOrbotV1642(t *testing.T) {
        lines := howtoLinesTor()
        all := strings.Join(lines, "\n")

        if !strings.Contains(all, "Orbot") {
                t.Errorf("how-to card must tell phones to use Orbot:\n%s", all)
        }
        if !strings.Contains(all, "127.0.0.1") || !strings.Contains(all, "9050") {
                t.Errorf("how-to card must name the Orbot SOCKS proxy host/port:\n%s", all)
        }
        if strings.Contains(all, `turn on "Use Tor"`) {
                t.Errorf("how-to card still tells phones to tick a Use Tor switch:\n%s", all)
        }
        if !strings.Contains(all, "no built-in Tor switch") {
                t.Errorf("how-to card should say the app has no built-in Tor switch:\n%s", all)
        }
}

// TestHowtoPCsHaveOwnTorV1642 pins the PC path: Windows, Linux and
// macOS are all covered, each of those PCs needs its own Tor, and the
// Tor Browser port 9150 shortcut is spelled out.
func TestHowtoPCsHaveOwnTorV1642(t *testing.T) {
        all := strings.Join(howtoLinesTor(), "\n")

        for _, os := range []string{"Windows", "Linux", "macOS"} {
                if !strings.Contains(all, os) {
                        t.Errorf("how-to card does not mention %s PCs:\n%s", os, all)
                }
        }
        if !strings.Contains(all, "own Tor") {
                t.Errorf("how-to card must say PCs need their own Tor:\n%s", all)
        }
        if !strings.Contains(all, "Tor Browser") || !strings.Contains(all, "9150") {
                t.Errorf("how-to card must give the Tor Browser SOCKS port 9150 shortcut:\n%s", all)
        }
}

// TestHowtoSocksPortMarkerV1642: the {{SOCKS-PORT}} marker line appears
// exactly once and the live label replaces it (the card builder swaps
// the marker for socksLbl).
func TestHowtoSocksPortMarkerV1642(t *testing.T) {
        n := 0
        for _, l := range howtoLinesTor() {
                if strings.TrimSpace(l) == "{{SOCKS-PORT}}" {
                        n++
                }
        }
        if n != 1 {
                t.Errorf("how-to card must carry exactly one {{SOCKS-PORT}} marker, found %d", n)
        }
}

// TestEndCursorV1642 pins the bottom-stick math: the cursor must land
// on the very end of the text (row = newline count, column = rune
// length of the last line), because ensureCursorVisible scrolls that
// position into view.
func TestEndCursorV1642(t *testing.T) {
        for _, c := range []struct {
                name      string
                text      string
                row, col  int
        }{
                {"empty", "", 0, 0},
                {"single line", "one line", 0, 8},
                {"two lines", "one\ntwo", 1, 3},
                {"trailing newline", "one\ntwo\n", 2, 0},
                {"multi", "a\nb\ncd", 2, 2},
                {"runes count as one column", "héllo\nwörld", 1, 5},
        } {
                t.Run(c.name, func(t *testing.T) {
                        row, col := endCursor(c.text)
                        if row != c.row || col != c.col {
                                t.Errorf("endCursor(%q) = row %d, col %d; want %d, %d", c.text, row, col, c.row, c.col)
                        }
                })
        }
}

// TestUserDirDefinedV1642 through TestEnsureXDGUserDirsIntegrationV1642
// (the v1.6.42 XDG user-dir tests) were removed in v1.6.46 together
// with internal/ui/xdguserdirs.go: creating $HOME folders to please
// the file dialog was retired - see v1646_test.go.
