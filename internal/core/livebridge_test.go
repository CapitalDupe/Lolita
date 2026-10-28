//go:build livebridge

// Live end-to-end reproduction of the Tor bridge bootstrap through the
// REAL sxops code paths (GenerateTorrc / FindTor / BootstrapStatus /
// TorBootstrapped), launched manually:
//
//   PATH=<dir with tor+obfs4proxy>:$PATH \
//   SXOPS_LIVE_BRIDGES=$'1.2.3.4:443 FINGERPRINT\n5.6.7.8:443 FP2' \
//   go test -tags livebridge -run TestLiveBridgeBootstrap -v -timeout 9m ./internal/core
//
// It prints every bootstrap phase transition (the exact percentages the
// Tor panel's ticker shows) and dumps the Tor notice log at the end, so
// a stall can be diagnosed from the log instead of guessed at.
package core

import (
        "fmt"
        "os"
        "os/exec"
        "path/filepath"
        "strings"
        "syscall"
        "testing"
        "time"
)

func liveEnv(key, def string) string {
        if v := os.Getenv(key); v != "" {
                return v
        }
        return def
}

func TestLiveBridgeBootstrap(t *testing.T) {
        raw := os.Getenv("SXOPS_LIVE_BRIDGES")
        var bridges []string
        for _, l := range strings.FieldsFunc(raw, func(r rune) bool { return r == '\n' || r == ',' }) {
                if l = strings.TrimSpace(l); l != "" {
                        bridges = append(bridges, l)
                }
        }
        if len(bridges) == 0 {
                t.Skip("SXOPS_LIVE_BRIDGES not set")
        }
        if err := ValidateBridges(bridges); err != nil {
                t.Fatalf("bridges do not validate: %v", err)
        }
        _, _ = runLiveTor(t, "", bridges, liveEnv("SXOPS_LIVE_DIR", "/home/z/my-project/scripts/live-bridge"))
}

// TestLiveDirectThenBridges reproduces the exact user flow: Tor runs
// DIRECT and is bootstrapped, then bridges are applied - stop, purge the
// directory cache, regenerate the torrc, restart - and the instance must
// re-bootstrap THROUGH the bridges (this is SetTorBridges' running path).
func TestLiveDirectThenBridges(t *testing.T) {
        raw := os.Getenv("SXOPS_LIVE_BRIDGES")
        var bridges []string
        for _, l := range strings.FieldsFunc(raw, func(r rune) bool { return r == '\n' || r == ',' }) {
                if l = strings.TrimSpace(l); l != "" {
                        bridges = append(bridges, l)
                }
        }
        if len(bridges) == 0 {
                t.Skip("SXOPS_LIVE_BRIDGES not set")
        }
        base := liveEnv("SXOPS_LIVE_DIR", "/home/z/my-project/scripts/live-bridge-switch")
        _ = os.RemoveAll(base)
        if err := os.MkdirAll(base, 0o700); err != nil {
                t.Fatal(err)
        }
        paths, err := NewPaths(base)
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
        tm := NewTorManager(paths, state, NopInstallLog())

        // phase 1: direct bootstrap (the state before the user applies bridges)
        tm2, cmd1 := runLiveTor(t, "", nil, base)
        _ = tm2

        // phase 2: the SetTorBridges running-Tor path - stop, purge, bridges, restart
        t.Logf("phase 2: stopping Tor, purging the directory cache, applying bridges")
        _ = syscall.Kill(-cmd1.Process.Pid, syscall.SIGINT)
        deadline := time.Now().Add(10 * time.Second)
        for cmd1.ProcessState == nil && time.Now().Before(deadline) {
                time.Sleep(200 * time.Millisecond)
        }
        if cmd1.ProcessState == nil {
                _ = syscall.Kill(-cmd1.Process.Pid, syscall.SIGKILL)
        }
        _ = os.Remove(filepath.Join(paths.TorDir(), "tor.pid"))

        tm.purgeDirectoryCache()
        s := state.Settings()
        s.TorBridges = bridges
        if err := state.SetSettings(s); err != nil {
                t.Fatal(err)
        }
        tm3, _ := runLiveTor(t, "restart", bridges, base)
        _ = tm3
}

// runLiveTor generates the torrc through the REAL GenerateTorrc (bridges
// set or cleared on the state first), starts tor like startServiceInstance
// does, and polls the REAL BootstrapStatus until 100% or timeout. The
// returned cmd lets the caller stop it for the switch scenario.
func runLiveTor(t *testing.T, tag string, bridges []string, base string) (*TorManager, *exec.Cmd) {
        t.Helper()
        paths, err := NewPaths(base)
        if err != nil {
                t.Fatal(err)
        }
        state, err := NewStateStore(paths)
        if err != nil {
                t.Fatal(err)
        }
        s := state.Settings()
        s.TorBridges = bridges
        if err := state.SetSettings(s); err != nil {
                t.Fatal(err)
        }
        tm := NewTorManager(paths, state, NopInstallLog())
        if err := tm.GenerateTorrc(); err != nil {
                t.Fatal(err)
        }
        if tag != "" {
                t.Logf("[%s] torrc regenerated with bridges", tag)
        }
        bin, err := tm.FindTor()
        if err != nil {
                t.Fatal(err)
        }
        logf, err := os.OpenFile(paths.TorLogFile(), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
        if err != nil {
                t.Fatal(err)
        }
        cmd := exec.Command(bin, "-f", filepath.Join(paths.TorDir(), "torrc"))
        cmd.Stdout = logf
        cmd.Stderr = logf
        prepareProcess(cmd)
        if err := cmd.Start(); err != nil {
                t.Fatalf("starting tor: %v", err)
        }
        logf.Close()

        last := ""
        start := time.Now()
        deadline := start.Add(5 * time.Minute)
        for time.Now().Before(deadline) {
                if cmd.ProcessState != nil {
                        t.Fatalf("[%s] tor exited early after %s", tag, time.Since(start).Round(time.Second))
                }
                if phase, ok := tm.BootstrapStatus(); ok && phase != last {
                        last = phase
                        t.Logf("[%s] [%s] bootstrap phase: %s", tag, time.Now().Format("15:04:05"), phase)
                        if TorBootstrapped(phase) {
                                t.Logf("[%s] REACHED 100%% after %s", tag, time.Since(start).Round(time.Second))
                                return tm, cmd
                        }
                }
                time.Sleep(1 * time.Second)
        }
        t.Errorf("[%s] DID NOT bootstrap within 5 minutes; last phase: %q", tag, last)
        data, _ := os.ReadFile(paths.TorLogFile())
        lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
        keep := 60
        if len(lines) < keep {
                keep = len(lines)
        }
        fmt.Fprintln(os.Stderr, "=== Tor notice log (last lines) ===")
        for _, l := range lines[len(lines)-keep:] {
                fmt.Fprintln(os.Stderr, l)
        }
        return tm, cmd
}
