package ui

// v1.6.44 tests: four operator requests on the "How to connect" guide:
//  1. the PC instructions are more detailed (Tor Browser download +
//     keep-open + the exact app setting + the 9050 service alternative);
//  2. the guide is clearer in general (PHONE / PC blocks with lettered
//     sub-steps, a check step, and what each pasted address does);
//  3. the card title dropped "(once)" - it reads "How to connect";
//  4. the guide mentions the xftp:// and chat relay (and ntf://)
//     addresses to paste in the app, not just smp://.

import (
        "strings"
        "testing"
)

// TestHowtoTitleDroppedOnceV1644 pins the title: exactly
// "How to connect", no "(once)".
func TestHowtoTitleDroppedOnceV1644(t *testing.T) {
        if got := howtoCardTitle(); got != "How to connect" {
                t.Errorf("howto card title = %q, want %q", got, "How to connect")
        }
        if strings.Contains(howtoCardTitle(), "(once)") {
                t.Errorf("howto card title still carries (once): %q", howtoCardTitle())
        }
}

// TestHowtoPCStepsDetailedV1644: the PC block walks through the whole
// flow - Tor Browser download source, keeping it open, the exact app
// setting to fill in (with host and port), the system-Tor 9050
// alternative with its Linux install command, and the sxops-machine
// shortcut.
func TestHowtoPCStepsDetailedV1644(t *testing.T) {
        all := strings.Join(howtoLinesTor(), "\n")

        for _, want := range []string{
                "PC (Windows / Linux / macOS)",
                "torproject.org",
                "install it",
                "Keep the Tor Browser OPEN",
                "Settings → Network & servers → SOCKS proxy",
                "127.0.0.1",
                "9150",
                "9050",
                "sudo apt install tor",
                "{{SOCKS-PORT}}",
        } {
                if !strings.Contains(all, want) {
                        t.Errorf("PC steps lost the detail %q:\n%s", want, all)
                }
        }
}

// TestHowtoCoversAllAddressKindsV1644: the guide tells the user what to
// paste and where - smp:// and xftp:// into SimpleX servers, the relay
// https link under Chat relay, and the ntf:// address under
// Notification servers.
func TestHowtoCoversAllAddressKindsV1644(t *testing.T) {
        all := strings.Join(howtoLinesTor(), "\n")

        for _, want := range []string{
                "smp://",
                "xftp://",
                "Chat relay",
                "https link",
                "ntf://",
                "Notification servers",
                "SimpleX servers",
        } {
                if !strings.Contains(all, want) {
                        t.Errorf("guide does not mention %q:\n%s", want, all)
                }
        }
}

// TestHowtoStructureClearV1644 pins the clearer structure: numbered
// steps 1..6, an explicit PHONE block, an explicit PC block, lettered
// sub-steps under both, and the final check step. Also pins the app
// install sources the operator asked for (Google Play / App Store /
// simplex.chat).
func TestHowtoStructureClearV1644(t *testing.T) {
        all := strings.Join(howtoLinesTor(), "\n")

        for _, want := range []string{
                "1. Install the SimpleX Chat app",
                "Google Play",
                "App Store",
                "2. The app has no built-in Tor switch",
                "3. Paste your addresses",
                "4. The chat relay is optional",
                "5. Check:",
                "6. To chat with someone",
                "PHONE (Android / iPhone)",
                "a. Install Orbot",
                "b. Open Orbot and press Start",
                "a. Download the Tor Browser",
        } {
                if !strings.Contains(all, want) {
                        t.Errorf("guide lost the structure piece %q:\n%s", want, all)
                }
        }
}
