package core

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestPGQueryEscape pins the libpq host= parameter encoding: "/" must be
// percent-encoded (the host parameter is a single value), spaces must be
// %20 - NOT "+" (form encoding, which libpq does not decode).
func TestPGQueryEscape(t *testing.T) {
	cases := map[string]string{
		"/home/z/.local/share/sxops/postgres/sock": "/home/z/.local/share/sxops/postgres/sock",
		"/tmp/pg sock": "/tmp/pg%20sock",
	}
	for in, want := range cases {
		got := pgQueryEscape(in)
		if strings.Contains(got, "+") {
			t.Errorf("pgQueryEscape(%q) = %q contains a raw + (libpq would not decode it as a space)", in, got)
		}
		// decoding %20 and %2F back must yield the original
		decoded := strings.ReplaceAll(strings.ReplaceAll(got, "%20", " "), "%2F", "/")
		if decoded != in {
			t.Errorf("pgQueryEscape(%q) = %q does not decode back to the input", in, got)
		}
		_ = want
	}
}

// TestNTFDBConnStrManaged pins the managed connection string: unix socket
// (no host:port), the sxops database and user, and the socket directory as
// the percent-encoded host= parameter. Skips when PostgreSQL binaries are
// present on the test machine only to assert the override path - the
// managed path is asserted whenever FindPostgresBinDir fails (CI has no
// PostgreSQL installed).
func TestNTFDBConnStr(t *testing.T) {
	paths, err := NewPaths(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	st, err := NewStateStore(paths)
	if err != nil {
		t.Fatal(err)
	}
	// advanced override always wins
	s := st.Settings()
	s.NTFDBConnection = "postgresql://external@db.example/ntf"
	if err := st.SetSettings(s); err != nil {
		t.Fatal(err)
	}
	if got := ntfDBConnStr(st, paths); got != s.NTFDBConnection {
		t.Errorf("override ignored: ntfDBConnStr = %q, want %q", got, s.NTFDBConnection)
	}
	// no override, no binaries: empty (the start path shows the hint)
	s.NTFDBConnection = ""
	if err := st.SetSettings(s); err != nil {
		t.Fatal(err)
	}
	if _, err := FindPostgresBinDir(); err != nil {
		if got := ntfDBConnStr(st, paths); got != "" {
			t.Errorf("ntfDBConnStr without PostgreSQL installed = %q, want empty", got)
		}
		t.Logf("no PostgreSQL on this machine - managed string not asserted")
		return
	}
	got := ntfDBConnStr(st, paths)
	want := "postgresql://" + ntfDBUser + "@/" + ntfDBName + "?host=" + pgQueryEscape(paths.PostgresSockDir())
	if got != want {
		t.Errorf("managed connstr = %q, want %q", got, want)
	}
	if !strings.HasPrefix(got, "postgresql://") || strings.Contains(got, "@localhost") {
		t.Errorf("managed connstr %q must be a socket-only URI", got)
	}
}

// TestApplyPortableProfileNTF pins the v1.5.7 INI profile for the NTF
// server: the database connection string and the SUBSCRIBER SOCKS proxy
// (its subscriptions to .onion SMP servers go through Tor, exactly like
// the chat relay's registration path), plus the control port. The old
// bogus "store_queues" key (an smp-server option the NTF server never
// read) must not be written.
func TestApplyPortableProfileNTF(t *testing.T) {
	ini := NewIni(t.TempDir() + "/ntf-server.ini")
	ini.appendSection("STORE_LOG")
	ini.appendSection("SUBSCRIBER")
	ini.appendSection("TRANSPORT")
	ini.appendSection("AUTH")

	rec := &ServerRec{Kind: KindNTF, Port: 5227}
	db := "postgresql://sxops_ntf@/ntf_server_store?host=%2Ftmp%2Fsock"
	applyPortableProfile(ini, KindNTF, Settings{}, rec, 9060, db)

	if got, ok := ini.Get("STORE_LOG", "db_connection"); !ok || got != db {
		t.Errorf("db_connection = %q (found=%v), want %q", got, ok, db)
	}
	if got, ok := ini.Get("SUBSCRIBER", "socks_proxy"); !ok || got != "127.0.0.1:9060" {
		t.Errorf("socks_proxy = %q (found=%v), want 127.0.0.1:9060", got, ok)
	}
	if got, ok := ini.Get("TRANSPORT", "control_port"); !ok || got != "5228" {
		t.Errorf("control_port = %q (found=%v), want 5228", got, ok)
	}
	if _, ok := ini.Get("STORE_LOG", "store_queues"); ok {
		t.Errorf("store_queues written for NTF - the NTF server has no such option")
	}
	// no SOCKS port known (Tor never connected): the key stays unset
	ini2 := NewIni(t.TempDir() + "/b.ini")
	ini2.appendSection("SUBSCRIBER")
	applyPortableProfile(ini2, KindNTF, Settings{}, rec, 0, db)
	if _, ok := ini2.Get("SUBSCRIBER", "socks_proxy"); ok {
		t.Errorf("socks_proxy written with no known SOCKS port")
	}
	// no database available: db_connection stays unset rather than
	// pointing the server at a broken default
	ini3 := NewIni(t.TempDir() + "/c.ini")
	ini3.appendSection("STORE_LOG")
	applyPortableProfile(ini3, KindNTF, Settings{}, rec, 9055, "")
	if _, ok := ini3.Get("STORE_LOG", "db_connection"); ok {
		t.Errorf("db_connection written with no database available")
	}
}

// TestApplyPortableProfileNonNTF pins that the new parameters never leak
// into the other servers' INIs.
func TestApplyPortableProfileNonNTF(t *testing.T) {
	for _, k := range []Kind{KindSMP, KindXFTP} {
		ini := NewIni(t.TempDir() + "/x.ini")
		ini.appendSection("STORE_LOG")
		ini.appendSection("SUBSCRIBER")
		applyPortableProfile(ini, k, Settings{}, &ServerRec{Kind: k}, 9060, "postgresql://x")
		if _, ok := ini.Get("SUBSCRIBER", "socks_proxy"); ok {
			t.Errorf("%s: socks_proxy must not be set", k)
		}
		if _, ok := ini.Get("STORE_LOG", "db_connection"); ok {
			t.Errorf("%s: db_connection must not be set", k)
		}
	}
}

// TestSettingsEnableNTFRoundTrip pins the persisted field name.
func TestSettingsEnableNTFRoundTrip(t *testing.T) {
	b, err := json.Marshal(Settings{EnableNTF: true})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `"enable_ntf":true`) {
		t.Errorf("field name changed: %s", b)
	}
	var s Settings
	if err := json.Unmarshal(b, &s); err != nil {
		t.Fatal(err)
	}
	if !s.EnableNTF {
		t.Error("EnableNTF lost in round trip")
	}
}

// TestFindPostgresBinDirMissing pins the install hint shown to the user
// (sxops needs the binaries only - the system service is never touched).
func TestFindPostgresBinDirMissing(t *testing.T) {
	dir, err := FindPostgresBinDir()
	if err == nil {
		t.Skipf("PostgreSQL found in %s on this machine", dir)
	}
	for _, want := range []string{"PostgreSQL", "sudo apt install postgresql"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error missing %q: %v", want, err)
		}
	}
}
