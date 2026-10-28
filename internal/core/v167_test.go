package core

import (
        "os"
        "path/filepath"
        "strings"
        "testing"
        "time"
)

// v1.6.7: XFTP file expiry and storage quota are exposed in the settings
// panel (persisted in state, written to the file server INI on every
// profile patch and applied live by restarting the running server), and
// the XFTP info web page gets its source code link: the SMP init command
// writes [INFORMATION] source_code via its --source-code flag, but the
// XFTP init command has no such flag and only leaves the key commented
// out - without it the XFTP info page renders no information section at
// all. The portable profile now asserts the key for SMP and XFTP (the
// NTF server does not read the INFORMATION section).

// xftpTemplateIni writes a file-server.ini shaped like the one the real
// xftp-server init produces (only the sections the profile touches).
func xftpTemplateIni(t *testing.T, path string) {
        t.Helper()
        content := `[INFORMATION]
# source_code = https://github.com/simplex-chat/simplexmq

[STORE_LOG]
enable = on
store_files = memory
expire_files_hours = 48
log_stats = off

[AUTH]
new_files = on
# control_port_admin_password =
# control_port_user_password =

[TRANSPORT]
host = <hostnames>
port = 443
# control_port = 5226

[FILES]
path = /files
storage_quota = 10gb

[WEB]
static_path = /www
# http = 8000
`
        if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
                t.Fatal(err)
        }
}

// TestApplyPortableProfileXFTP pins the v1.6.7 XFTP profile: source code
// link activated, file expiry and storage quota written from settings,
// info web port enabled, control tokens generated ONCE (a re-patch must
// never rotate them - the state records and any running server would be
// left holding stale credentials).
func TestApplyPortableProfileXFTP(t *testing.T) {
        path := filepath.Join(t.TempDir(), "file-server.ini")
        xftpTemplateIni(t, path)
        ini, err := LoadIni(path)
        if err != nil {
                t.Fatal(err)
        }
        s := Settings{XFTPExpireHours: 168, XFTPQuotaGB: 25, EnableInfoPages: true}
        applyPortableProfile(ini, KindXFTP, s, &ServerRec{Kind: KindXFTP}, 0, "")

        if got, ok := ini.Get("INFORMATION", "source_code"); !ok || got != simplexmqSourceRepo {
                t.Errorf("source_code = %q (found=%v), want %q", got, ok, simplexmqSourceRepo)
        }
        if got, ok := ini.Get("STORE_LOG", "expire_files_hours"); !ok || got != "168" {
                t.Errorf("expire_files_hours = %q (found=%v), want 168", got, ok)
        }
        if got, ok := ini.Get("FILES", "storage_quota"); !ok || got != "25gb" {
                t.Errorf("storage_quota = %q (found=%v), want 25gb", got, ok)
        }
        if got, ok := ini.Get("WEB", "http"); !ok || got != "8025" {
                t.Errorf("WEB http = %q (found=%v), want 8025", got, ok)
        }
        admin, okAdmin := ini.Get("AUTH", "control_port_admin_password")
        user, okUser := ini.Get("AUTH", "control_port_user_password")
        if !okAdmin || !okUser || admin == "" || user == "" {
                t.Fatalf("control tokens not generated (admin=%q user=%q)", admin, user)
        }
        if got, ok := ini.Get("TRANSPORT", "control_port"); !ok || got != "5226" {
                t.Errorf("control_port = %q (found=%v), want 5226", got, ok)
        }
        if err := ini.Save(); err != nil {
                t.Fatal(err)
        }

        // re-patch from the SAVED file: defaults apply and the control
        // tokens must not rotate (the state records would go stale)
        ini2, err := LoadIni(path)
        if err != nil {
                t.Fatal(err)
        }
        applyPortableProfile(ini2, KindXFTP, Settings{}, &ServerRec{Kind: KindXFTP}, 0, "")
        if got, _ := ini2.Get("STORE_LOG", "expire_files_hours"); got != "48" {
                t.Errorf("default expire_files_hours = %q, want 48", got)
        }
        // v1.6.8: quota off is the default - the key must be commented
        // out (absent for the server = no limit), never an empty value
        if _, ok := ini2.Get("FILES", "storage_quota"); ok {
                t.Errorf("default storage_quota must be disabled (off), got %q", ini2.Text())
        }
        admin2, _ := ini2.Get("AUTH", "control_port_admin_password")
        if admin2 != admin {
                t.Errorf("control token rotated on re-patch (%q -> %q)", admin, admin2)
        }

        // info pages off: the web port key is commented out again
        ini3, _ := LoadIni(path)
        applyPortableProfile(ini3, KindXFTP, Settings{}, &ServerRec{Kind: KindXFTP}, 0, "")
        if _, ok := ini3.Get("INFORMATION", "source_code"); !ok {
                t.Error("source_code lost")
        }
        if _, ok := ini3.Get("WEB", "http"); ok {
                t.Error("WEB http must stay disabled with info pages off")
        }
}

// TestApplyPortableProfileSMPSourceCode: the SMP INI already carries the
// source code written by init --source-code; the profile asserts the
// same value (idempotent) and never writes XFTP keys into it.
func TestApplyPortableProfileSMPSourceCode(t *testing.T) {
        path := filepath.Join(t.TempDir(), "smp-server.ini")
        content := `[INFORMATION]
source_code = https://github.com/simplex-chat/simplexmq

[STORE_LOG]
enable = on

[AUTH]
# control_port_admin_password =

[TRANSPORT]
port = 5223
control_port = 5224
`
        if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
                t.Fatal(err)
        }
        ini, err := LoadIni(path)
        if err != nil {
                t.Fatal(err)
        }
        applyPortableProfile(ini, KindSMP, Settings{EnableInfoPages: true}, &ServerRec{Kind: KindSMP}, 0, "")
        if got, ok := ini.Get("INFORMATION", "source_code"); !ok || got != simplexmqSourceRepo {
                t.Errorf("source_code = %q (found=%v), want %q", got, ok, simplexmqSourceRepo)
        }
        if _, ok := ini.Get("STORE_LOG", "expire_files_hours"); ok {
                t.Error("expire_files_hours must not be written for SMP")
        }
        if _, ok := ini.Get("FILES", "storage_quota"); ok {
                t.Error("storage_quota must not be written for SMP")
        }
        if _, ok := ini.Get("AUTH", "control_port_admin_password"); ok {
                t.Error("SMP control tokens must not be generated by the profile (init writes them)")
        }
}

// TestStateXFTPExpireDefault: a state file from before v1.6.7 (no
// xftp_expire_hours field) gets the 48h default on load; a v1 state
// carrying the old silent 10 GB quota default is migrated to off (0)
// once - the v1.6.8 default.
func TestStateXFTPExpireDefault(t *testing.T) {
        paths := testPaths(t)
        stateJSON := `{
  "version": 1,
  "settings": {"relay_name": "Old", "xftp_quota_gb": 10},
  "servers": {}
}`
        if err := os.WriteFile(paths.StateFile(), []byte(stateJSON), 0o600); err != nil {
                t.Fatal(err)
        }
        state, err := NewStateStore(paths)
        if err != nil {
                t.Fatal(err)
        }
        s := state.Settings()
        if s.XFTPExpireHours != 48 {
                t.Errorf("XFTPExpireHours = %d, want 48 (default for pre-1.6.7 state)", s.XFTPExpireHours)
        }
        if s.XFTPQuotaGB != 0 {
                t.Errorf("XFTPQuotaGB = %d, want 0 (old silent default 10 migrated to off)", s.XFTPQuotaGB)
        }
        if state.Get().Version != 2 {
                t.Errorf("state version = %d, want 2 (migrated)", state.Get().Version)
        }
}

// TestInitArgsXFTPQuota pins the init argv for the quota setting
// (0 = off: init still REQUIRES a --quota value, so a placeholder is
// passed and the INI patch comments the key out right after; an
// explicit value passes through).
func TestInitArgsXFTPQuota(t *testing.T) {
        paths := testPaths(t)
        for _, tc := range []struct {
                quota int
                want  string
        }{
                {0, "10gb"},
                {10, "10gb"},
                {25, "25gb"},
                {100, "100gb"},
        } {
                args, err := InitArgs(KindXFTP, Settings{XFTPQuotaGB: tc.quota}, paths)
                if err != nil {
                        t.Fatal(err)
                }
                found := false
                for i, a := range args {
                        if a == "--quota" && i+1 < len(args) {
                                found = args[i+1] == tc.want
                        }
                }
                if !found {
                        t.Errorf("quota %d: --quota %s not in argv %v", tc.quota, tc.want, args)
                }
        }
}

// TestReloadServerConfig: with the XFTP server installed and running,
// ReloadServerConfig re-patches the INI (source code, expiry, quota)
// and restarts the process; the control tokens in state stay stable.
func TestReloadServerConfig(t *testing.T) {
        paths := testPaths(t)
        state, err := NewStateStore(paths)
        if err != nil {
                t.Fatal(err)
        }
        if err := state.SetSettings(Settings{
                PublicHost:      "example.com",
                XFTPExpireHours: 72,
                XFTPQuotaGB:     25,
                EnableInfoPages: true,
        }); err != nil {
                t.Fatal(err)
        }
        _ = state.UpdateServer(KindXFTP, func(r *ServerRec) {
                r.Installed = true
                r.Enabled = true
                r.Fingerprint = "XFTPFPXFTPFP"
                r.ControlAdmin = "old-admin-token"
                r.ControlUser = "old-user-token"
        })
        sv := NewSupervisor(paths, state)
        eng := &Engine{Paths: paths, State: state, Sv: sv, Log: NopInstallLog(),
                Tor: NewTorManager(paths, state, NopInstallLog())}
        eng.Backend = &nativeBackend{eng: eng}

        if err := paths.Ensure(); err != nil {
                t.Fatal(err)
        }

        // a realistic file-server.ini (as written by init) + fingerprint file
        iniPath := filepath.Join(paths.CfgDir(KindXFTP), "file-server.ini")
        xftpTemplateIni(t, iniPath)
        if err := os.WriteFile(filepath.Join(paths.CfgDir(KindXFTP), "fingerprint"), []byte("XFTPFPXFTPFP\n"), 0o600); err != nil {
                t.Fatal(err)
        }

        // stub the binary and start it under the supervisor
        bin := paths.BinPath(KindXFTP)
        if err := os.MkdirAll(filepath.Dir(bin), 0o700); err != nil {
                t.Fatal(err)
        }
        if err := os.WriteFile(bin, []byte("#!/bin/sh\nsleep 120\n"), 0o755); err != nil {
                t.Fatal(err)
        }
        if err := sv.StartWithArgs(KindXFTP, StartArgs(KindXFTP), nil); err != nil {
                t.Fatal(err)
        }
        t.Cleanup(func() { _ = sv.Stop(KindXFTP, 5*time.Second) })

        if err := eng.ReloadServerConfig(KindXFTP); err != nil {
                t.Fatalf("ReloadServerConfig: %v", err)
        }

        // the INI now carries the current settings
        data, err := os.ReadFile(iniPath)
        if err != nil {
                t.Fatal(err)
        }
        text := string(data)
        for _, want := range []string{
                "source_code = " + simplexmqSourceRepo,
                "expire_files_hours = 72",
                "storage_quota = 25gb",
        } {
                if !strings.Contains(text, want) {
                        t.Errorf("patched INI missing %q\n---\n%s", want, text)
                }
        }

        // the server was restarted (running again under the supervisor)
        deadline := time.Now().Add(10 * time.Second)
        for time.Now().Before(deadline) {
                if sv.Status(KindXFTP).State == ProcRunning {
                        break
                }
                time.Sleep(100 * time.Millisecond)
        }
        if st := sv.Status(KindXFTP).State; st != ProcRunning {
                t.Fatalf("XFTP server not running after reload: %v", st)
        }

        // control tokens kept stable in state (not rotated)
        rec := state.Server(KindXFTP)
        if rec.ControlAdmin == "" || rec.ControlUser == "" {
                t.Fatal("control tokens missing from state after reload")
        }
}

// TestReloadServerConfigSkipsNotInstalled: nothing to reload when the
// server is not installed yet - the settings apply at provisioning.
func TestReloadServerConfigSkipsNotInstalled(t *testing.T) {
        paths := testPaths(t)
        state, err := NewStateStore(paths)
        if err != nil {
                t.Fatal(err)
        }
        sv := NewSupervisor(paths, state)
        eng := &Engine{Paths: paths, State: state, Sv: sv, Log: NopInstallLog()}
        eng.Backend = &nativeBackend{eng: eng}
        if err := eng.ReloadServerConfig(KindXFTP); err != nil {
                t.Fatalf("ReloadServerConfig on a not-installed server should be a no-op, got: %v", err)
        }
        if err := eng.ReloadServerConfig(KindRelay); err == nil {
                t.Fatal("ReloadServerConfig must refuse the chat relay (no INI)")
        }
}
