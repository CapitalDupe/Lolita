package core

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// v1634 recording backend: implements Backend with canned Tor state and
// records which Tor lifecycle methods were called, so the bridge-apply
// flow can be asserted without a real Tor process.
type v1634Backend struct {
	running  bool
	started  int
	stopped  int
	failStop bool
}

func (b *v1634Backend) Name() string                                  { return "v1634" }
func (b *v1634Backend) InstallBinary(context.Context, Kind, *Release) error { return nil }
func (b *v1634Backend) InitServer(Kind, []string) (string, error)     { return "", nil }
func (b *v1634Backend) StartServer(Kind) error                        { return nil }
func (b *v1634Backend) StopServer(Kind) error                         { return nil }
func (b *v1634Backend) ServerRunning(Kind) bool                       { return false }
func (b *v1634Backend) ReadServerFile(Kind, string) ([]byte, error)   { return nil, os.ErrNotExist }
func (b *v1634Backend) WriteServerFile(Kind, string, []byte) error    { return nil }
func (b *v1634Backend) ServerLogTail(Kind, int) ([]byte, error)       { return nil, os.ErrNotExist }
func (b *v1634Backend) WriteTorrc(string) error                       { return nil }
func (b *v1634Backend) StartTor(context.Context) error {
	b.started++
	return nil
}
func (b *v1634Backend) StopTor() error {
	b.stopped++
	if b.failStop {
		return os.ErrPermission
	}
	return nil
}
func (b *v1634Backend) TorRunning() bool               { return b.running }
func (b *v1634Backend) BootstrapStatus() (string, bool) { return "", false }
func (b *v1634Backend) ReadOnionHost(Kind) (string, error) {
	return "", os.ErrNotExist
}
func (b *v1634Backend) NeedsTorInstall() (bool, string) { return false, "" }
func (b *v1634Backend) InstallTor(context.Context) error {
	return nil
}
func (b *v1634Backend) TorLogTail(int) ([]byte, error) { return nil, os.ErrNotExist }
func (b *v1634Backend) ReadStoreLog(Kind, int) ([]byte, error) {
	return nil, os.ErrNotExist
}

func v1634Engine(t *testing.T) (*Engine, *v1634Backend) {
	t.Helper()
	eng, _, _ := newTestEngine(t)
	b := &v1634Backend{}
	eng.Backend = b
	return eng, b
}

// TestSetTorBridgesNoStartWhenTorOff pins the v1.6.34 rule: applying
// bridges while Tor is off must NOT start it. v1.6.33 always relaunched
// Tor (RetryTor) from the bridges section, turning a config edit into a
// minutes-long bootstrap the user never asked for.
func TestSetTorBridgesNoStartWhenTorOff(t *testing.T) {
	eng, b := v1634Engine(t)

	restarted, err := eng.SetTorBridges(context.Background(), []string{"203.0.113.9:443 abcdef"})
	if err != nil {
		t.Fatalf("SetTorBridges: %v", err)
	}
	if restarted {
		t.Errorf("restarted = true with Tor off; want false (Tor must not start)")
	}
	if b.started != 0 || b.stopped != 0 {
		t.Errorf("backend called StartTor %d / StopTor %d times; want 0/0 (Tor was off)", b.started, b.stopped)
	}
	// the lines are persisted and the torrc is ready for the next Connect
	if br := eng.State.Settings().TorBridges; len(br) != 1 || br[0] != "203.0.113.9:443 abcdef" {
		t.Fatalf("settings.TorBridges = %v", br)
	}
	data, err := os.ReadFile(filepath.Join(eng.Paths.TorDir(), "torrc"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "UseBridges 1\n") || !strings.Contains(string(data), "Bridge 203.0.113.9:443 abcdef\n") {
		t.Errorf("torrc does not carry the bridges:\n%s", string(data))
	}
}

// TestSetTorBridgesRestartsWhenRunning: a running Tor is stopped and
// started again so the new bridge lines take effect (a running Tor never
// re-reads its torrc).
func TestSetTorBridgesRestartsWhenRunning(t *testing.T) {
	eng, b := v1634Engine(t)
	b.running = true

	restarted, err := eng.SetTorBridges(context.Background(), []string{"203.0.113.9:443 abcdef"})
	if err != nil {
		t.Fatalf("SetTorBridges: %v", err)
	}
	if !restarted {
		t.Errorf("restarted = false with Tor running; want true")
	}
	if b.stopped != 1 {
		t.Errorf("StopTor called %d times; want 1", b.stopped)
	}
	if b.started != 1 {
		t.Errorf("StartTor called %d times; want 1 (the restart)", b.started)
	}
}

// TestSetTorBridgesPurgesTorCache pins the v1.6.34 fix for "bridges
// applied through the UI leave Tor stuck at 25%": the cached consensus /
// descriptors / guard state were fetched over the previous (direct)
// network path; a bridge change now drops them so the next bootstrap
// fetches everything through the NEW bridges. The hidden-service keys
// live in tor/hs and must survive.
func TestSetTorBridgesPurgesTorCache(t *testing.T) {
	eng, _ := v1634Engine(t)

	seed := func(dir string) {
		t.Helper()
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		for _, f := range []string{"cached-certs", "cached-microdesc-consensus", "cached-microdescs", "cached-microdescs.new", "state", "lock"} {
			if err := os.WriteFile(filepath.Join(dir, f), []byte("stale"), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		// a transport-plugin state dir that must survive the purge
		if err := os.MkdirAll(filepath.Join(dir, "pt_state"), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "pt_state", "obfs4_state.json"), []byte("{}"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	seed(eng.Paths.TorDataDir())
	seed(eng.Paths.TorClientDataDir())
	// hidden-service keys must be untouched by the purge
	hs := eng.Paths.HiddenServiceDir(KindSMP)
	if err := os.MkdirAll(hs, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(hs, "hostname"), []byte("abcdefghij567onion.onion\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := eng.SetTorBridges(context.Background(), []string{"203.0.113.9:443 abcdef"}); err != nil {
		t.Fatalf("SetTorBridges: %v", err)
	}

	check := func(dir string) {
		t.Helper()
		for _, f := range []string{"cached-certs", "cached-microdesc-consensus", "cached-microdescs", "state", "lock"} {
			if _, err := os.Stat(filepath.Join(dir, f)); err == nil {
				t.Errorf("%s/%s survived the purge (dir %s)", filepath.Base(dir), f, dir)
			}
		}
		if _, err := os.Stat(filepath.Join(dir, "pt_state", "obfs4_state.json")); err != nil {
			t.Errorf("pt_state was removed by the purge (dir %s): %v", dir, err)
		}
	}
	check(eng.Paths.TorDataDir())
	check(eng.Paths.TorClientDataDir())

	host, err := os.ReadFile(filepath.Join(hs, "hostname"))
	if err != nil || strings.TrimSpace(string(host)) != "abcdefghij567onion.onion" {
		t.Errorf("hidden-service hostname did not survive the purge: %q, %v", string(host), err)
	}
}

// TestGenerateClientTorrcBridges: the client-only instance (single-hop
// mode) gets the same bridge lines as the service instance, and no
// transport-plugin line appears for transports the bridges do not use.
func TestGenerateClientTorrcBridges(t *testing.T) {
	eng, _ := v1634Engine(t)
	s := eng.State.Settings()
	s.SingleHopOnion = true
	s.TorBridges = []string{"203.0.113.9:443 abcdef"}
	if err := eng.State.SetSettings(s); err != nil {
		t.Fatal(err)
	}
	if err := eng.Tor.GenerateTorrc(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(eng.Paths.TorClientRcPath())
	if err != nil {
		t.Fatal(err)
	}
	c := string(data)
	if !strings.Contains(c, "UseBridges 1\n") || !strings.Contains(c, "Bridge 203.0.113.9:443 abcdef\n") {
		t.Errorf("client torrc missing the bridges:\n%s", c)
	}
	if strings.Contains(c, "ClientTransportPlugin") {
		t.Errorf("client torrc has a transport plugin for vanilla bridges (obfs4proxy is not installed here):\n%s", c)
	}
}

// TestSetTorBridgesStopError: a failing stop is reported, not swallowed.
func TestSetTorBridgesStopError(t *testing.T) {
	eng, b := v1634Engine(t)
	b.running = true
	b.failStop = true

	if _, err := eng.SetTorBridges(context.Background(), []string{"203.0.113.9:443 abcdef"}); err == nil {
		t.Fatalf("a failing StopTor must surface an error")
	}
}
