package core

import (
        "os"
        "path/filepath"
        "strings"
        "testing"
)

// v1.6.5: the "Connect through Tor SOCKS proxy" setting was removed. It
// only ever toggled the --socks-proxy flag of `smp-server init` (which
// writes socks_mode = onion into smp-server.ini) - a flag that matters
// only when the SMP server itself dials out to .onion targets as a
// routing proxy, was on by default and matches the official server
// docs. It never affected the onion services, the chat relay (which
// always passes --socks-proxy/-x for .onion targets on its own) or
// sxops' own probes, and flipping it after init changed nothing (the
// INI is never re-written for it). InitArgs now always passes the flag
// and the settings struct no longer has the field; old state files
// that still carry enable_socks_proxy keep loading (unknown JSON
// fields are ignored) and drop it on the next save.

func TestInitArgsAlwaysSocksProxy(t *testing.T) {
        paths := testPaths(t)
        // Both password variants must carry the flag: there is no
        // setting left that could turn it off.
        for _, s := range []Settings{
                {PasswordProtect: false},
                {PasswordProtect: true, QueuePassword: "pw"},
        } {
                args, err := InitArgs(KindSMP, s, paths)
                if err != nil {
                        t.Fatalf("InitArgs(SMP): %v", err)
                }
                found := false
                for _, a := range args {
                        if a == "--socks-proxy" {
                                found = true
                        }
                }
                if !found {
                        t.Errorf("InitArgs(SMP, PasswordProtect=%v) = %v without --socks-proxy", s.PasswordProtect, args)
                }
        }
        // The other server kinds have no such flag (their binaries do
        // not even accept it).
        for _, k := range []Kind{KindXFTP, KindNTF} {
                args, err := InitArgs(k, Settings{XFTPQuotaGB: 10}, paths)
                if err != nil {
                        t.Fatalf("InitArgs(%s): %v", k, err)
                }
                for _, a := range args {
                        if a == "--socks-proxy" {
                                t.Errorf("InitArgs(%s) unexpectedly passes --socks-proxy", k)
                        }
                }
        }
}

func TestStateDropsRemovedSocksSetting(t *testing.T) {
        paths := testPaths(t)
        // A v1.6.4 state file: settings carried enable_socks_proxy.
        old := `{
  "version": 1,
  "settings": {
    "password_protect": false,
    "relay_name": "My Relay",
    "relay_use_own_smp": true,
    "relay_web_enabled": false,
    "enable_info_pages": true,
    "single_hop_onion": false,
    "auto_restart": true,
    "release_channel": "stable",
    "enable_socks_proxy": true
  },
  "servers": {},
  "tor": {"managed": false, "socks_port": 0, "control_port": 0, "onion_ready": false, "connect_failed": false},
  "provisioned": true
}`
        if err := os.WriteFile(paths.StateFile(), []byte(old), 0o600); err != nil {
                t.Fatal(err)
        }

        st, err := NewStateStore(paths)
        if err != nil {
                t.Fatalf("state with enable_socks_proxy no longer loads: %v", err)
        }
        if s := st.Settings(); s.RelayName != "My Relay" || !s.EnableInfoPages {
                t.Fatalf("settings misparsed after removal: %+v", s)
        }
        // Any save (here: SetSettings with the very same values) must
        // drop the removed key from the file.
        if err := st.SetSettings(st.Settings()); err != nil {
                t.Fatal(err)
        }
        data, err := os.ReadFile(paths.StateFile())
        if err != nil {
                t.Fatal(err)
        }
        if strings.Contains(string(data), "enable_socks_proxy") {
                t.Errorf("enable_socks_proxy survived the save:\n%s", filepath.Join(paths.Root, "state.json"))
        }
}
