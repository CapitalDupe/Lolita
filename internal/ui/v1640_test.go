package ui

// v1.6.40 tests: the Server stats settings note now explains exactly
// WHERE the daily totals are saved - the two monospace log-file path
// lines under the selector (operator request: explain the location in
// the note instead of adding anything to the UI). The file names and
// the portable LOG directory are pinned against upstream simplexmq.

import (
        "path/filepath"
        "strings"
        "testing"

        "fyne.io/fyne/v2"

        "sxops/internal/core"
)

// TestServerStatsNoteSaysWhereV1640: the note head ends by pointing at
// the LOCAL log files (a colon leads into the two path lines), and it
// no longer claims the totals are "written to LOCAL log files" without
// saying which ones.
func TestServerStatsNoteSaysWhereV1640(t *testing.T) {
        head := serverStatsNote()
        if !strings.Contains(head, "these LOCAL log files") {
                t.Errorf("note head lost the file-location pointer:\n%s", head)
        }
        if !strings.HasSuffix(head, ":") {
                t.Errorf("note head should end with ':' leading into the path lines:\n%s", head)
        }
        if strings.Contains(head, "public info page") {
                t.Errorf("note head should not carry the public-page sentence (it is in the tail):\n%s", head)
        }
}

// TestServerStatsSettingLabelKeptV1640 pins the revert of the attempted
// caption rename: the operator asked for "Server stats (Log):", then
// decided to keep the original v1.6.39 "Collect daily server
// statistics:" - the (Log) hint lives only in the note and the exact
// paths below it.
func TestServerStatsSettingLabelKeptV1640(t *testing.T) {
        if got := serverStatsSettingLabel(); got != "Collect daily server statistics:" {
                t.Errorf("setting label = %q, want %q (rename reverted)", got, "Collect daily server statistics:")
        }
}

// TestStatsDailyLogNameV1640 pins the daily stats file names as
// hardcoded upstream (simplexmq v6.5.0 / stable):
//   - smp:  Server/Main.hs              "smp-server-stats.daily.log"
//   - xftp: FileTransfer/Server/Main.hs "file-server-stats.daily.log"
//   - ntf:  Notifications/Server/Main.hs "ntf-server-stats.daily.log"
//   - relay: not a simplexmq server - none.
func TestStatsDailyLogNameV1640(t *testing.T) {
        for _, c := range []struct {
                kind core.Kind
                want string
        }{
                {core.KindSMP, "smp-server-stats.daily.log"},
                {core.KindXFTP, "file-server-stats.daily.log"},
                {core.KindNTF, "ntf-server-stats.daily.log"},
                {core.KindRelay, ""},
        } {
                if got := core.StatsDailyLogName(c.kind); got != c.want {
                        t.Errorf("StatsDailyLogName(%s) = %q, want %q", c.kind, got, c.want)
                }
        }
}

// TestStatsLogFilePortablePathV1640: the path shown in Settings is the
// server's portable LOG directory (set via the *_SERVER_LOG_PATH env
// var) plus the upstream file name - i.e. exactly where the running
// server writes the daily CSV.
func TestStatsLogFilePortablePathV1640(t *testing.T) {
        p, err := core.NewPaths(t.TempDir())
        if err != nil {
                t.Fatal(err)
        }
        for _, c := range []struct {
                kind core.Kind
                want string
        }{
                {core.KindSMP, filepath.Join("servers", "smp", "var", "smp-server-stats.daily.log")},
                {core.KindXFTP, filepath.Join("servers", "xftp", "var", "file-server-stats.daily.log")},
                {core.KindNTF, filepath.Join("servers", "ntf", "var", "ntf-server-stats.daily.log")},
                {core.KindRelay, ""},
        } {
                got := p.StatsLogFile(c.kind)
                want := ""
                if c.want != "" {
                        want = filepath.Join(p.Root, c.want)
                }
                if got != want {
                        t.Errorf("StatsLogFile(%s) = %q, want %q", c.kind, got, want)
                }
        }
}

// TestStatsPathLabelV1640: each path renders with its SMP:/XFTP:
// prefix on its own monospace, character-wrapped line (mirrors how the
// data folder path is rendered in the same view), so a long path can
// never stretch the window.
func TestStatsPathLabelV1640(t *testing.T) {
        p, err := core.NewPaths(t.TempDir())
        if err != nil {
                t.Fatal(err)
        }
        for _, c := range []struct {
                kind core.Kind
                pfx  string
        }{
                {core.KindSMP, "SMP: "},
                {core.KindXFTP, "XFTP: "},
        } {
                l := statsPathLabel(c.pfx, p.StatsLogFile(c.kind))
                if !strings.HasPrefix(l.Text, c.pfx) {
                        t.Errorf("path label for %s lost its %q prefix: %q", c.kind, c.pfx, l.Text)
                }
                if !strings.HasSuffix(l.Text, core.StatsDailyLogName(c.kind)) {
                        t.Errorf("path label for %s must end with the stats file name: %q", c.kind, l.Text)
                }
                if !l.TextStyle.Monospace {
                        t.Errorf("path label for %s must be monospace", c.kind)
                }
                if l.Wrapping != fyne.TextWrapBreak {
                        t.Errorf("path label for %s must character-wrap (TextWrapBreak), got %v", c.kind, l.Wrapping)
                }
        }
}
