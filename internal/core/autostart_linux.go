package core

import (
	"os"
	"os/exec"
	"path/filepath"
)

const daemonUnitName = "sxops-daemon"

// v1.5.8: sxops never starts anything automatically anymore. Tor and the
// servers start only on an explicit action (the dashboard Start buttons,
// Connect in the Tor tab, or "sxops start" / "sxops daemon" on the CLI).
// The registration helpers below exist only to find and remove whatever
// older versions left behind.

// DisableAutoStart removes autostart registration (systemd user unit and
// XDG autostart entry - whichever exists).
func DisableAutoStart() error {
	dir, err := os.UserConfigDir()
	if err != nil {
		return err
	}
	_ = exec.Command("systemctl", "--user", "disable", "--now", daemonUnitName).Run()
	removals := []string{
		filepath.Join(dir, "systemd", "user", daemonUnitName+".service"),
		filepath.Join(dir, "autostart", "sxops-daemon.desktop"),
	}
	for _, p := range removals {
		if _, err := os.Stat(p); err == nil {
			if err := os.Remove(p); err != nil {
				return err
			}
		}
	}
	_ = exec.Command("systemctl", "--user", "daemon-reload").Run()
	return nil
}

// AutoStartEnabled reports whether a legacy autostart registration exists.
func AutoStartEnabled() bool {
	dir, err := os.UserConfigDir()
	if err != nil {
		return false
	}
	for _, p := range []string{
		filepath.Join(dir, "systemd", "user", daemonUnitName+".service"),
		filepath.Join(dir, "autostart", "sxops-daemon.desktop"),
	} {
		if _, err := os.Stat(p); err == nil {
			return true
		}
	}
	return false
}

// removeLegacyAutostart unregisters the login autostart created by older
// sxops versions ("Start servers automatically when I log in" ran the
// daemon - and with it Tor and the servers - at every login; on WSL that
// fires the moment the app launches the WSL session). Called from
// NewEngine: a cheap no-op (two file stats) once clean, and it keeps
// re-checking so a leftover entry never survives an sxops run.
func removeLegacyAutostart(log InstallLog) {
	if !AutoStartEnabled() {
		return
	}
	if err := DisableAutoStart(); err != nil {
		log.Detail("removing the old login autostart failed: " + err.Error())
		return
	}
	log.Detail("removed the old \"start servers at login\" registration - sxops now starts Tor and the servers only when you press Start")
}
