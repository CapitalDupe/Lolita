package core

// v1.6.41 core test: turning "Show server web pages" OFF in Settings
// must also disable the XFTP server's info web page - the [WEB] http
// key is commented out in file-server.ini and the onion virtual-80
// mapping disappears from the managed torrc. (Before v1.6.41 the
// toggle only reloaded the SMP server, so a running file server kept
// serving its info page until some unrelated restart.)

import (
        "os"
        "path/filepath"
        "strings"
        "testing"
)

// TestXFTPWebPageFollowsInfoPagesToggleV1641: with pages on, the XFTP
// profile asserts [WEB] http and the torrc maps virtual port 80; with
// pages off (the only change), a re-patch comments the key out and the
// regenerated torrc drops the mapping.
func TestXFTPWebPageFollowsInfoPagesToggleV1641(t *testing.T) {
        eng, paths, state := queuesTestEngine(t, KindXFTP, xftpTemplateIni)

        // a real install records the web port (the torrc maps the onion
        // virtual-80 target from it)
        if err := state.UpdateServer(KindXFTP, func(r *ServerRec) {
                r.WebPort = DefaultWebPort(KindXFTP)
        }); err != nil {
                t.Fatal(err)
        }

        // pages ON (the default settings state)
        if !state.Settings().EnableInfoPages {
                t.Fatal("test premise: pages default to on")
        }
        if _, err := eng.patchServerIniChanged(KindXFTP); err != nil {
                t.Fatalf("patch with pages on: %v", err)
        }
        ini := readFileT(t, filepath.Join(paths.CfgDir(KindXFTP), Def(KindXFTP).IniName))
        if !strings.Contains(ini, "http = "+itoa(DefaultWebPort(KindXFTP))) {
                t.Errorf("pages on: [WEB] http must be active in file-server.ini:\n%s", ini)
        }
        if err := eng.Tor.GenerateTorrc(); err != nil {
                t.Fatal(err)
        }
        torrc := readFileT(t, filepath.Join(paths.TorDir(), "torrc"))
        if !strings.Contains(torrc, "HiddenServicePort 80 127.0.0.1:") {
                t.Errorf("pages on: torrc must map onion virtual port 80:\n%s", torrc)
        }

        // pages OFF - the v1.6.41 flow re-patches the XFTP INI too
        s := state.Settings()
        s.EnableInfoPages = false
        if err := state.SetSettings(s); err != nil {
                t.Fatal(err)
        }
        if _, err := eng.patchServerIniChanged(KindXFTP); err != nil {
                t.Fatalf("patch with pages off: %v", err)
        }
        ini = readFileT(t, filepath.Join(paths.CfgDir(KindXFTP), Def(KindXFTP).IniName))
        if v, ok := loadIniStringT(t, ini, "WEB", "http"); ok && strings.TrimSpace(v) != "" {
                t.Errorf("pages off: [WEB] http must be commented out in file-server.ini, got %q:\n%s", v, ini)
        }
        if err := eng.Tor.GenerateTorrc(); err != nil {
                t.Fatal(err)
        }
        torrc = readFileT(t, filepath.Join(paths.TorDir(), "torrc"))
        if strings.Contains(torrc, "HiddenServicePort 80 ") {
                t.Errorf("pages off: torrc must drop the onion virtual-80 mapping:\n%s", torrc)
        }
}

// loadIniStringT reads one key out of an INI held in memory (nil-safe
// for the commented-out case the test checks).
func loadIniStringT(t *testing.T, content, section, key string) (string, bool) {
        t.Helper()
        tmp := filepath.Join(t.TempDir(), "v1641.ini")
        if err := os.WriteFile(tmp, []byte(content), 0o600); err != nil {
                t.Fatal(err)
        }
        ini, err := LoadIni(tmp)
        if err != nil {
                t.Fatal(err)
        }
        return ini.Get(section, key)
}
