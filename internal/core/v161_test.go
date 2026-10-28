package core

import (
        "strings"
        "testing"
)

// testPaths builds an isolated Paths under a temp dir.
func testPaths(t *testing.T) *Paths {
        t.Helper()
        paths, err := NewPaths(t.TempDir())
        if err != nil {
                t.Fatal(err)
        }
        return paths
}

// v1.6.1: the chat relay must never run — long-running or one-shot —
// unless it is locked to the own SMP server. When the own SMP address is
// not found (empty or placeholder), every CLI invocation is refused
// BEFORE the binary is executed: without the lock the CLI would fall
// back to its preset list of official SimpleX servers.

func TestOwnSMPNotFound(t *testing.T) {
        cases := []struct {
                addr string
                want bool
        }{
                {"", true},
                {"smp://YOUR-SERVER-HOST", true},
                {"smp://fp@YOUR-SERVER-HOST", true},
                {"smp://fp@abc.onion", false},
                {"smp://fp@example.com,abc.onion:5223", false},
        }
        for _, c := range cases {
                if got := ownSMPNotFound(c.addr); got != c.want {
                        t.Errorf("ownSMPNotFound(%q) = %v, want %v", c.addr, got, c.want)
                }
        }
}

func TestRelayAddressFromDBRefusedWithoutOwnSMP(t *testing.T) {
        paths := testPaths(t)
        for _, addr := range []string{"", "smp://YOUR-SERVER-HOST", "smp://fp@YOUR-SERVER-HOST:5223"} {
                _, err := RelayAddressFromDB(paths, addr, 0)
                if err == nil {
                        t.Fatalf("RelayAddressFromDB(%q) unexpectedly succeeded", addr)
                }
                for _, want := range []string{"own SMP server not found", "no fallback"} {
                        if !strings.Contains(err.Error(), want) {
                                t.Errorf("RelayAddressFromDB(%q) error %q does not mention %q", addr, err, want)
                        }
                }
        }
}

func TestRelayCommandRefusedWithoutOwnSMP(t *testing.T) {
        paths := testPaths(t)
        st, err := NewStateStore(paths)
        if err != nil {
                t.Fatal(err)
        }
        // fresh state: no SMP server record -> no own address
        _, err = RelayCommand(paths, st, "/sa")
        if err == nil {
                t.Fatal("RelayCommand unexpectedly succeeded without an own SMP server")
        }
        if !strings.Contains(err.Error(), "own SMP server not found") {
                t.Errorf("unexpected error: %v", err)
        }
}

func TestRelayLinkTestErr(t *testing.T) {
        const own = "smp://fp@ownhost.onion"
        cases := []struct {
                smp, link, wantSub string
                wantErr            bool
        }{
                // own SMP not found -> never tested, no connection
                {"", "https://ownhost.onion/r#key", "own SMP server not found", true},
                {"smp://fp@YOUR-SERVER-HOST", "https://ownhost.onion/r#key", "own SMP server not found", true},
                // foreign (official) host -> never contacted
                {own, "https://smp6.simplex.im/r#key", "not your server", true},
                // own host -> safe to test
                {own, "https://ownhost.onion/r#key", "", false},
        }
        for _, c := range cases {
                reason := relayLinkTestErr(c.smp, c.link)
                if (reason != "") != c.wantErr {
                        t.Errorf("relayLinkTestErr(%q, %q) = %q, wantErr %v", c.smp, c.link, reason, c.wantErr)
                        continue
                }
                if c.wantSub != "" && !strings.Contains(reason, c.wantSub) {
                        t.Errorf("relayLinkTestErr(%q, %q) = %q, want substring %q", c.smp, c.link, reason, c.wantSub)
                }
        }
}

// The relay argv keeps the v1.6.0 lock: -s plus --relay-address-server
// pinned to the own server whenever an address exists.
func TestRelayArgsLockOwnServer(t *testing.T) {
        paths := testPaths(t)
        s := defaultSettings()
        const addr = "smp://FP@ownhost.onion"
        args := strings.Join(RelayArgs(s, paths, addr, 9150), " ")
        if !strings.Contains(args, "-s "+addr) {
                t.Errorf("relay argv missing -s lock: %s", args)
        }
        if !strings.Contains(args, "--relay-address-server "+addr) {
                t.Errorf("relay argv missing --relay-address-server: %s", args)
        }
}
