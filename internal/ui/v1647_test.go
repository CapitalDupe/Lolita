package ui

// v1.6.47 tests: the connection guide's PC background-Tor alternative
// now also covers Windows via the Tor Expert Bundle (torproject.org,
// unzip and run tor.exe) instead of leaving Windows users with the
// full Tor Browser as their only option; the SimpleX app's SOCKS proxy
// then points at 127.0.0.1:9050 like any system Tor. README.md and
// README-WSL.txt carry the same mention.

import (
        "os"
        "strings"
        "testing"
)

// TestHowtoExpertBundleV1647: the PC alternative sub-step names the Tor
// Expert Bundle with its download source, its executable and the port
// to use, and the Linux command survives the rewording.
func TestHowtoExpertBundleV1647(t *testing.T) {
        all := strings.Join(howtoLinesTor(), "\n")

        for _, want := range []string{
                "Tor Expert Bundle",
                "torproject.org",
                "tor.exe",
                "9050",
                "sudo apt install tor",
        } {
                if !strings.Contains(all, want) {
                        t.Errorf("PC alternative lost the detail %q:\n%s", want, all)
                }
        }

        // the bundle is offered as the Windows branch of the alternative
        pcBlock := all[strings.Index(all, "PC (Windows"):]
        if i := strings.Index(pcBlock, "Tor Expert Bundle"); i < 0 {
                t.Fatalf("Expert Bundle is not inside the PC block")
        }
        // phones stay Orbot-only (v1.6.43 pin, re-checked after rewording)
        phoneBlock := all[strings.Index(all, "PHONE"):strings.Index(all, "PC (")]
        for _, banned := range []string{"Tor Expert Bundle", "tor.exe", "9050", "SOCKS"} {
                if strings.Contains(phoneBlock, banned) {
                        t.Errorf("phone block must not mention %q:\n%s", banned, phoneBlock)
                }
        }
}

// TestReadmeMentionsExpertBundleV1647: README.md and README-WSL.txt
// carry the same Windows alternative as the dashboard card.
func TestReadmeMentionsExpertBundleV1647(t *testing.T) {
        for _, f := range []string{"../../README.md", "../../README-WSL.txt"} {
                src, err := os.ReadFile(f)
                if err != nil {
                        t.Skipf("%s not readable: %v", f, err)
                }
                s := string(src)
                if !strings.Contains(s, "Tor Expert Bundle") {
                        t.Errorf("%s does not mention the Tor Expert Bundle", f)
                }
                if !strings.Contains(s, "tor.exe") {
                        t.Errorf("%s does not mention tor.exe", f)
                }
                if !strings.Contains(s, "9050") {
                        t.Errorf("%s does not mention the 9050 proxy port", f)
                }
        }
}
