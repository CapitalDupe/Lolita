package ui

// v1.6.45 tests: the operator asked to remove the "Save state to disk"
// button from the admin panel - it sends the "save" control command,
// which only makes the server flush its state to disk; nothing visible
// changes in the app, so the button looked dead. The removal retires
// the button, the saveBtn field, the capability probe, and the "save"
// entry in the control command table (FetchRaw/Run both gate on that
// table, so "save" becomes unsendable - the server command itself is
// untouched, sxops just never issues it).

import (
	"os"
	"strings"
	"testing"

	"sxops/internal/core"
)

// TestAdminSaveButtonGoneV1645: no trace of the button survives in the
// admin view source (source-scan pattern, like the v1.6.21+ wording
// pins) - neither the button text nor the field, while the Settings
// view's unrelated "Save settings" button is out of this file's scope.
func TestAdminSaveButtonGoneV1645(t *testing.T) {
	src, err := os.ReadFile("admin.go")
	if err != nil {
		t.Skipf("source not readable: %v", err)
	}
	s := string(src)
	for _, gone := range []string{
		"Save state to disk",
		"saveBtn",
		`runAdmin("save", "")`,
		`ControlSupported(k, "save")`,
	} {
		if strings.Contains(s, gone) {
			t.Errorf("admin.go must no longer contain %q", gone)
		}
	}
}

// TestSaveCommandUnlistedV1645: "save" is gone from every control
// command table entry, so ControlSupported reports false everywhere
// and FetchRaw/Run reject it before ever opening a connection.
func TestSaveCommandUnlistedV1645(t *testing.T) {
	for _, k := range []core.Kind{core.KindSMP, core.KindXFTP, core.KindNTF, core.KindRelay} {
		if core.ControlSupported(k, "save") {
			t.Errorf("ControlSupported(%s, \"save\") = true, want false", core.Def(k).Name)
		}
	}
}

// TestBlockStatusCommandsStillListedV1645: the removal took only
// "save" - the commands the moderation tools rely on (block, unblock,
// status, delete) stay listed for the servers that support them.
func TestBlockStatusCommandsStillListedV1645(t *testing.T) {
	for _, c := range []string{"block", "unblock", "status", "delete"} {
		if !core.ControlSupported(core.KindSMP, c) {
			t.Errorf("ControlSupported(SMP, %q) = false, want true", c)
		}
	}
	for _, c := range []string{"block", "delete"} {
		if !core.ControlSupported(core.KindXFTP, c) {
			t.Errorf("ControlSupported(XFTP, %q) = false, want true", c)
		}
	}
}
