package core

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNormalizeBridgeLines(t *testing.T) {
	in := []string{
		"  obfs4 203.0.113.1:443 cert=abc iat-mode=0  ",
		"",
		"Bridge 203.0.113.2:443 1234abcd",
		"   ",
		"# a comment",
	}
	want := []string{
		"obfs4 203.0.113.1:443 cert=abc iat-mode=0",
		"203.0.113.2:443 1234abcd",
	}
	got := NormalizeBridgeLines(in)
	if len(got) != len(want) {
		t.Fatalf("NormalizeBridgeLines: got %d lines, want %d: %v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("line %d: got %q, want %q", i, got[i], want[i])
		}
	}
}

func TestValidateBridgesVanillaOK(t *testing.T) {
	if err := ValidateBridges([]string{"203.0.113.2:443 1234abcd"}); err != nil {
		t.Fatalf("vanilla bridge should validate: %v", err)
	}
}

func TestValidateBridgesUnsupported(t *testing.T) {
	for _, bad := range []string{"snowflake 1 2", "meek_lite 1 2", "webtunnel 1 2"} {
		if err := ValidateBridges([]string{bad}); err == nil {
			t.Errorf("%q should be rejected", bad)
		}
	}
}

func TestGenerateTorrcWithBridges(t *testing.T) {
	dir := t.TempDir()
	paths, err := NewPaths(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := paths.Ensure(); err != nil {
		t.Fatal(err)
	}
	state, err := NewStateStore(paths)
	if err != nil {
		t.Fatal(err)
	}
	s := state.Settings()
	s.TorBridges = []string{"203.0.113.9:443 abcdef"}
	if err := state.SetSettings(s); err != nil {
		t.Fatal(err)
	}
	tm := NewTorManager(paths, state, NopInstallLog())
	if err := tm.GenerateTorrc(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(paths.TorDir(), "torrc"))
	if err != nil {
		t.Fatal(err)
	}
	torrc := string(data)
	if !strings.Contains(torrc, "UseBridges 1\n") {
		t.Errorf("torrc missing UseBridges 1:\n%s", torrc)
	}
	if !strings.Contains(torrc, "Bridge 203.0.113.9:443 abcdef\n") {
		t.Errorf("torrc missing Bridge line:\n%s", torrc)
	}
}

func TestGenerateTorrcWithoutBridges(t *testing.T) {
	dir := t.TempDir()
	paths, _ := NewPaths(dir)
	if err := paths.Ensure(); err != nil {
		t.Fatal(err)
	}
	state, err := NewStateStore(paths)
	if err != nil {
		t.Fatal(err)
	}
	tm := NewTorManager(paths, state, NopInstallLog())
	if err := tm.GenerateTorrc(); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(filepath.Join(paths.TorDir(), "torrc"))
	if strings.Contains(string(data), "UseBridges") {
		t.Errorf("torrc must not contain UseBridges without bridges:\n%s", string(data))
	}
}
