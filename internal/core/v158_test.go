package core

import (
	"os"
	"path/filepath"
	"testing"
)

// v1.5.8: sxops starts nothing automatically anymore. The login
// autostart older versions registered must be found and removed by
// every engine construction. XDG_CONFIG_HOME is redirected to a temp
// dir so the test never touches the real user config.
func TestRemoveLegacyAutostart(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)

	if AutoStartEnabled() {
		t.Fatal("a clean config must report no autostart registration")
	}
	// no-op path: nothing registered, nothing to do, no panic
	removeLegacyAutostart(NopInstallLog())

	// plant the legacy XDG autostart entry an old version left behind
	auto := filepath.Join(dir, "autostart")
	if err := os.MkdirAll(auto, 0o700); err != nil {
		t.Fatal(err)
	}
	desktop := filepath.Join(auto, "sxops-daemon.desktop")
	if err := os.WriteFile(desktop, []byte("[Desktop Entry]\nType=Application\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if !AutoStartEnabled() {
		t.Fatal("the planted legacy entry must be detected")
	}

	removeLegacyAutostart(NopInstallLog())

	if AutoStartEnabled() {
		t.Fatal("the legacy entry must be gone after the cleanup")
	}
	if _, err := os.Stat(desktop); !os.IsNotExist(err) {
		t.Fatal("the desktop file itself must be deleted")
	}
	// and the cleanup must stay a cheap no-op afterwards
	removeLegacyAutostart(NopInstallLog())
}
