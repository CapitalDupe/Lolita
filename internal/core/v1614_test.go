package core

import (
        "os"
        "path/filepath"
        "strings"
        "testing"
)

// v1.6.14: every official ban method collected behind one Ban button
// with a check-mark list - the queue block (reason + notice lifetime) is
// always the core action, optional extras are the permanent delete and
// the server-wide [AUTH] new_queues / new_files registration gate. The
// notice lifetimes were re-tuned: 2 days replaces 1 day.

// TestNewLinksRoundTripSMP: blocking sets [AUTH] new_queues = off in the
// smp-server.ini (rewriting the commented default into an active
// assignment), reading the state back live, and un-blocking sets it back
// to on. The server is not running in the test, so no restart happens -
// the INI applies at the next start, exactly like production stopped use.
func TestNewLinksRoundTripSMP(t *testing.T) {
        eng, paths, _ := queuesTestEngine(t, KindSMP, smpTemplateIni)
        iniPath := filepath.Join(paths.CfgDir(KindSMP), Def(KindSMP).IniName)

        blocked, err := eng.NewLinksBlocked(KindSMP)
        if err != nil {
                t.Fatalf("initial read: %v", err)
        }
        if blocked {
                t.Fatal("gate must read allowed on the commented default")
        }

        if err := eng.SetNewLinksBlocked(KindSMP, true); err != nil {
                t.Fatalf("blocking: %v", err)
        }
        data, err := os.ReadFile(iniPath)
        if err != nil {
                t.Fatal(err)
        }
        if !strings.Contains(string(data), "new_queues = off") {
                t.Fatalf("INI must contain an active 'new_queues = off', got:\n%s", data)
        }
        blocked, err = eng.NewLinksBlocked(KindSMP)
        if err != nil || !blocked {
                t.Fatalf("after blocking: blocked=%v err=%v", blocked, err)
        }

        if err := eng.SetNewLinksBlocked(KindSMP, false); err != nil {
                t.Fatalf("un-blocking: %v", err)
        }
        blocked, err = eng.NewLinksBlocked(KindSMP)
        if err != nil || blocked {
                t.Fatalf("after un-blocking: blocked=%v err=%v", blocked, err)
        }
}

// TestNewLinksRoundTripXFTP: same for the file server's new_files key,
// with the upstream template that already carries "new_files = on"
// (an active assignment is rewritten in place).
func TestNewLinksRoundTripXFTP(t *testing.T) {
        eng, paths, _ := queuesTestEngine(t, KindXFTP, xftpTemplateIni)
        iniPath := filepath.Join(paths.CfgDir(KindXFTP), Def(KindXFTP).IniName)

        if err := eng.SetNewLinksBlocked(KindXFTP, true); err != nil {
                t.Fatalf("blocking: %v", err)
        }
        data, _ := os.ReadFile(iniPath)
        if strings.Count(string(data), "new_files") != 1 || !strings.Contains(string(data), "new_files = off") {
                t.Fatalf("expected exactly one new_files assignment set to off:\n%s", data)
        }
        blocked, err := eng.NewLinksBlocked(KindXFTP)
        if err != nil || !blocked {
                t.Fatalf("after blocking: blocked=%v err=%v", blocked, err)
        }
}

// TestNewLinksUnsupported: the notification server has no such gate and
// every call refuses instead of touching anything.
func TestNewLinksUnsupported(t *testing.T) {
        eng, _, _ := queuesTestEngine(t, KindNTF, nil)
        if NewLinksSupported(KindNTF) {
                t.Fatal("NTF must not support new-link blocking")
        }
        if NewLinksSupported(KindRelay) {
                t.Fatal("the chat relay must not support new-link blocking")
        }
        if !NewLinksSupported(KindSMP) || !NewLinksSupported(KindXFTP) {
                t.Fatal("SMP and XFTP must support new-link blocking")
        }
        if _, err := eng.NewLinksBlocked(KindNTF); err == nil {
                t.Fatal("NewLinksBlocked(NTF) must error")
        }
        if err := eng.SetNewLinksBlocked(KindNTF, true); err == nil {
                t.Fatal("SetNewLinksBlocked(NTF) must error")
        }
}

// TestNewLinksNotInstalled: no INI to toggle before the setup ran.
func TestNewLinksNotInstalled(t *testing.T) {
        eng, _, _ := queuesTestEngine(t, KindSMP, nil)
        _ = eng.State.UpdateServer(KindSMP, func(r *ServerRec) { r.Installed = false })
        if _, err := eng.NewLinksBlocked(KindSMP); err == nil {
                t.Fatal("NewLinksBlocked on a not-installed server must error")
        }
        if err := eng.SetNewLinksBlocked(KindSMP, true); err == nil {
                t.Fatal("SetNewLinksBlocked on a not-installed server must error")
        }
}
