package core

// v1.6.32: StopAll must stop EVERY server - the close-window "Stop
// servers" choice used to fire it in a goroutine that died with the
// app process, and the sequential stops (relay, SMP, XFTP, NTF, Tor)
// meant everything after SMP was left running (the operator's report:
// XFTP stayed up while every other server stopped). The close flow now
// waits for StopAll, and StopAll signals all three servers at once and
// waits for them together.

import (
        "fmt"
        "os"
        "os/exec"
        "path/filepath"
        "syscall"
        "testing"
        "time"
)

// TestStopAllStopsEveryServerV1632 starts real supervised stub
// processes for SMP, XFTP and NTF, calls StopAll and asserts every one
// of them is stopped - none left running - once StopAll returns, and
// that no pid file survives (the next sxops start must not think a
// server is still up).
func TestStopAllStopsEveryServerV1632(t *testing.T) {
        paths := testPaths(t)
        state, err := NewStateStore(paths)
        if err != nil {
                t.Fatal(err)
        }
        sv := NewSupervisor(paths, state)
        eng := &Engine{Paths: paths, State: state, Sv: sv, Log: NopInstallLog(),
                Tor: NewTorManager(paths, state, NopInstallLog())}
        eng.Backend = &nativeBackend{eng: eng}
        if err := paths.Ensure(); err != nil {
                t.Fatal(err)
        }

        // one stub binary per kind - a plain sleep dies on SIGINT just
        // like a real server shutting down; the supervisor only starts
        // servers the state marks installed
        for _, k := range []Kind{KindSMP, KindXFTP, KindNTF} {
                _ = state.UpdateServer(k, func(r *ServerRec) {
                        r.Installed = true
                        r.Enabled = true
                })
                bin := paths.BinPath(k)
                if err := os.MkdirAll(filepath.Dir(bin), 0o700); err != nil {
                        t.Fatal(err)
                }
                if err := os.WriteFile(bin, []byte("#!/bin/sh\nsleep 120\n"), 0o755); err != nil {
                        t.Fatal(err)
                }
                if err := sv.StartWithArgs(k, StartArgs(k), nil); err != nil {
                        t.Fatalf("start %s stub: %v", Def(k).Name, err)
                }
        }
        for _, k := range []Kind{KindSMP, KindXFTP, KindNTF} {
                if err := sv.WaitRunning(k, 10*time.Second); err != nil {
                        t.Fatalf("%s stub did not start: %v", Def(k).Name, err)
                }
        }

        // StopAll(false): Tor is not running (nothing to stop) and no
        // relay was started - exactly the close-window situation minus Tor
        eng.StopAll(false)

        // EVERY server must be down once StopAll returns - the v1.6.31
        // bug was XFTP still running here
        for _, k := range []Kind{KindSMP, KindXFTP, KindNTF} {
                if st := sv.Status(k); st.State != ProcStopped && st.State != ProcFailed {
                        t.Errorf("%s still %v after StopAll - the close flow would leave it running", Def(k).Name, st.State)
                }
                if _, ok := readPidFile(paths.PidFile(k)); ok {
                        t.Errorf("%s pid file survived StopAll", Def(k).Name)
                }
        }

        // idempotent: a second StopAll (the poll loop can race one in)
        // must not hang or error
        done := make(chan struct{})
        go func() { eng.StopAll(false); close(done) }()
        select {
        case <-done:
        case <-time.After(15 * time.Second):
                t.Fatal("second StopAll hung")
        }
}

// TestStopAllStopsAdoptedServersV1632: the close flow's StopAll must
// also stop servers a PREVIOUS sxops instance left running (the pid
// file path - "keep running in the background" then reopen sxops and
// choose Stop servers). A raw process is planted with its pid file the
// way the old instance leaves it, and StopAll must reap it.
func TestStopAllStopsAdoptedServersV1632(t *testing.T) {
        paths := testPaths(t)
        state, err := NewStateStore(paths)
        if err != nil {
                t.Fatal(err)
        }
        sv := NewSupervisor(paths, state)
        eng := &Engine{Paths: paths, State: state, Sv: sv, Log: NopInstallLog(),
                Tor: NewTorManager(paths, state, NopInstallLog())}
        eng.Backend = &nativeBackend{eng: eng}
        if err := paths.Ensure(); err != nil {
                t.Fatal(err)
        }

        // plant an adopted XFTP server: a detached sleep with its own
        // process group (what prepareProcess gives real servers) and a
        // pid file pointing at the group leader
        attr := &syscall.SysProcAttr{Setpgid: true}
        cmd := exec.Command("/bin/sh", "-c", "sleep 120")
        cmd.SysProcAttr = attr
        if err := cmd.Start(); err != nil {
                t.Fatal(err)
        }
        pid := cmd.Process.Pid
        t.Cleanup(func() { _ = cmd.Process.Kill(); _, _ = cmd.Process.Wait() })
        if err := os.MkdirAll(filepath.Dir(paths.PidFile(KindXFTP)), 0o700); err != nil {
                t.Fatal(err)
        }
        if err := os.WriteFile(paths.PidFile(KindXFTP), []byte(fmt.Sprintf("%d\n", pid)), 0o600); err != nil {
                t.Fatal(err)
        }

        // reap in the background from the start: the planted process is
        // the TEST's child, and until it is waited for it stays a
        // zombie that answers kill(pid,0) with success - stopAdopted's
        // pidAlive polling and the assertion below both need the reap
        // (in real life the previous sxops instance was the parent and
        // init reaps, so pidAlive is accurate there)
        wch := make(chan struct{})
        go func() { _, _ = cmd.Process.Wait(); close(wch) }()

        eng.StopAll(false)

        select {
        case <-wch:
        case <-time.After(3 * time.Second):
                t.Fatal("the adopted XFTP server survived StopAll - reopening sxops and choosing Stop servers would leave it running")
        }
        if _, ok := readPidFile(paths.PidFile(KindXFTP)); ok {
                t.Error("the adopted server's pid file survived StopAll")
        }
}
