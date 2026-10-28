package core

// v1.6.39 tests: the ServerStats setting ("off"/"smp"/"xftp"/"both",
// default off) decides [STORE_LOG] log_stats per server - the upstream
// switch for the daily statistics collected into LOCAL log files (the
// public info page shows only a "Stats enabled" line, never the
// numbers). sxops used to force log_stats on for the SMP server; the
// profile now writes the value explicitly for SMP and XFTP, and the
// NTF server keeps it on (local log only).

import (
        "os"
        "path/filepath"
        "strings"
        "testing"
)

// smpStatsTemplateIni writes an smp-server.ini shaped like the real
// init output (log_stats = on is what sxops wrote before v1.6.39).
func smpStatsTemplateIni(t *testing.T, path string) {
        t.Helper()
        content := `[INFORMATION]
source_code = https://github.com/simplex-chat/simplexmq

[STORE_LOG]
enable = on
store_msgs = off
log_stats = on

[AUTH]
new_queues = on

[TRANSPORT]
host = <hostnames>
port = 5223

[WEB]
static_path = /www
# http = 8000
`
        if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
                t.Fatal(err)
        }
}

func TestServerStatsEnabled(t *testing.T) {
        cases := []struct {
                setting string
                smp     bool
                xftp    bool
                ntf     bool
        }{
                {"off", false, false, false},
                {"smp", true, false, false},
                {"xftp", false, true, false},
                {"both", true, true, false},
                {"", false, false, false},      // old state: no field yet
                {"garbage", false, false, false}, // hand-edited state
        }
        for _, c := range cases {
                s := Settings{ServerStats: c.setting}
                if got := s.ServerStatsEnabled(KindSMP); got != c.smp {
                        t.Errorf("ServerStatsEnabled(SMP) with %q = %v, want %v", c.setting, got, c.smp)
                }
                if got := s.ServerStatsEnabled(KindXFTP); got != c.xftp {
                        t.Errorf("ServerStatsEnabled(XFTP) with %q = %v, want %v", c.setting, got, c.xftp)
                }
                if got := s.ServerStatsEnabled(KindNTF); got != c.ntf {
                        t.Errorf("ServerStatsEnabled(NTF) with %q = %v, want %v", c.setting, got, c.ntf)
                }
        }
}

// TestApplyPortableProfileServerStats pins the full matrix: the profile
// writes log_stats per the setting for SMP and XFTP (overriding the
// on sxops itself wrote before v1.6.39 - the setting is the source of
// truth now), and NTF stays on regardless of the setting.
func TestApplyPortableProfileServerStats(t *testing.T) {
        want := map[string]map[Kind]string{
                "off": {KindSMP: "off", KindXFTP: "off", KindNTF: "on"},
                "smp": {KindSMP: "on", KindXFTP: "off", KindNTF: "on"},
                "xftp": {KindSMP: "off", KindXFTP: "on", KindNTF: "on"},
                "both": {KindSMP: "on", KindXFTP: "on", KindNTF: "on"},
        }
        for setting, perKind := range want {
                for _, k := range []Kind{KindSMP, KindXFTP, KindNTF} {
                        path := filepath.Join(t.TempDir(), Def(k).IniName)
                        if k == KindXFTP {
                                xftpTemplateIni(t, path) // log_stats = off in the template
                        } else {
                                smpStatsTemplateIni(t, path) // log_stats = on in the template
                        }
                        ini, err := LoadIni(path)
                        if err != nil {
                                t.Fatal(err)
                        }
                        s := Settings{ServerStats: setting, EnableInfoPages: true}
                        applyPortableProfile(ini, k, s, &ServerRec{Kind: k}, 0, "")
                        got, ok := ini.Get("STORE_LOG", "log_stats")
                        if !ok {
                                t.Fatalf("%s/%s: log_stats key missing", setting, k)
                        }
                        if got != perKind[k] {
                                t.Errorf("%s: %s log_stats = %q, want %q", setting, k, got, perKind[k])
                        }
                }
        }

        // The gate key the operator may set by hand stays untouched next to
        // the stats key (v1.6.12 regression guard still holds).
        path := filepath.Join(t.TempDir(), "smp-server.ini")
        smpStatsTemplateIni(t, path)
        ini, err := LoadIni(path)
        if err != nil {
                t.Fatal(err)
        }
        ini.Set("AUTH", "new_queues", "off")
        applyPortableProfile(ini, KindSMP, Settings{ServerStats: "smp"}, &ServerRec{}, 0, "")
        if !strings.Contains(ini.Text(), "new_queues = off") {
                t.Fatalf("profile dropped the auth gate:\n%s", ini.Text())
        }
}

// TestDefaultSettingsServerStats: fresh settings default to off, and a
// loaded state with an unknown value is coerced back to off.
func TestDefaultSettingsServerStats(t *testing.T) {
        if got := defaultSettings().ServerStats; got != "off" {
                t.Errorf("default ServerStats = %q, want off", got)
        }
        st := &StateStore{state: &State{Settings: Settings{ServerStats: "nope"}}}
        st.applyDefaults()
        if got := st.state.Settings.ServerStats; got != "off" {
                t.Errorf("applyDefaults kept %q, want off", got)
        }
        st2 := &StateStore{state: &State{Settings: Settings{ServerStats: "both"}}}
        st2.applyDefaults()
        if got := st2.state.Settings.ServerStats; got != "both" {
                t.Errorf("applyDefaults clobbered a valid value: %q", got)
        }
}
