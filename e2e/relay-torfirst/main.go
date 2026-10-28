// E2E harness: verify Tor-first startup (no direct fallback).
//
//  1. GATE: with the Tor binary unusable, StartAll must FAIL and no
//     server may be running afterwards.
//  2. TOR-FIRST: with Tor killed and the relay's address wiped (fresh
//     one-shot registration), StartComponent(SMP) must START TOR itself;
//     StartRelay must then register via the SMP onion through Tor and
//     the https address must be created.
package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"sxops/internal/core"
)

func main() {
	eng, err := core.NewEngine(core.NopInstallLog())
	if err != nil {
		panic(err)
	}
	// the tor binary sxops would use: managed install first, PATH second
	torPath := eng.Paths.TorBinPath()
	if _, serr := os.Stat(torPath); serr != nil {
		if p, lerr := exec.LookPath("tor"); lerr == nil {
			torPath = p
		}
	}
	fmt.Println("== tor binary under test:", torPath)

	// ---- clean slate: everything (incl. Tor) stopped ----
	fmt.Println("== stopping everything (incl. Tor)")
	eng.StopAll(true)
	for i := 0; i < 15 && eng.Tor.Running(); i++ {
		time.Sleep(time.Second)
		eng.StopAll(true)
	}
	fmt.Println("== Tor running?", eng.Tor.Running())

	// ---- 1. GATE: Tor unusable -> StartAll fails, nothing starts ----
	fmt.Println("== GATE: making the Tor binary unusable + stripping PATH")
	realPath := os.Getenv("PATH")
	if err := os.Chmod(torPath, 0o644); err != nil {
		panic(err)
	}
	os.Setenv("PATH", "/nonexistent")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	err = eng.StartAll(ctx)
	cancel()
	os.Setenv("PATH", realPath)
	_ = os.Chmod(torPath, 0o755)
	fmt.Println("== StartAll error:", firstLine(err))
	if err == nil {
		fmt.Println("FAIL: StartAll should fail when Tor cannot start")
		os.Exit(1)
	}
	if !strings.Contains(strings.ToLower(err.Error()), "tor") {
		fmt.Println("FAIL: error should mention Tor:", err)
		os.Exit(1)
	}
	smpUp := eng.Backend.ServerRunning(core.KindSMP)
	xftpUp := eng.Backend.ServerRunning(core.KindXFTP)
	relayUp := eng.Sv.Status(core.KindRelay).State == core.ProcRunning
	fmt.Printf("== after failed StartAll: smp=%v xftp=%v relay=%v (all must be false)\n", smpUp, xftpUp, relayUp)
	if smpUp || xftpUp || relayUp {
		fmt.Println("FAIL: servers must NOT start when Tor cannot")
		os.Exit(1)
	}
	torState := eng.State.Get().Tor
	fmt.Printf("== Tor state after failure: ConnectFailed=%v (must be true)\n", torState.ConnectFailed)
	if !torState.ConnectFailed {
		fmt.Println("FAIL: ConnectFailed should be recorded for the Connect/Retry button logic")
		os.Exit(1)
	}
	fmt.Println("GATE OK: servers start only once Tor is running")

	// ---- 2. TOR-FIRST: fresh relay registration via the onion ----
	fmt.Println("== wiping the relay address + DB for a fresh registration")
	_ = eng.State.UpdateServer(core.KindRelay, func(r *core.ServerRec) { r.Fingerprint = "" })
	if err := os.MkdirAll(eng.Paths.RelayDir(), 0o700); err != nil {
		panic(err)
	}
	entries, _ := os.ReadDir(eng.Paths.RelayDir())
	for _, e := range entries {
		_ = os.RemoveAll(filepath.Join(eng.Paths.RelayDir(), e.Name()))
	}

	fmt.Println("== starting SMP with Tor down (sxops must start Tor first)")
	if err := eng.StartComponent(core.KindSMP); err != nil {
		fmt.Println("FAIL: StartComponent(SMP):", err)
		os.Exit(1)
	}
	if !eng.Tor.Running() {
		fmt.Println("FAIL: Tor should have been started by StartComponent")
		os.Exit(1)
	}
	if eng.State.Get().Tor.ConnectFailed {
		fmt.Println("FAIL: ConnectFailed should be cleared after the successful bootstrap")
		os.Exit(1)
	}
	fmt.Println("== Tor running now?", eng.Tor.Running(), "(must be true)")

	// After a Tor restart the hidden-service descriptor takes minutes to
	// propagate - poll until the SMP onion answers (this mirrors a user
	// pressing Start again once the Tor tab shows connected).
	fmt.Println("== waiting for the SMP onion to become reachable through Tor")
	smpAddr0 := core.Address(core.KindSMP, eng.State.Settings(), eng.State.Server(core.KindSMP))
	ready := false
	for i := 0; i < 60; i++ {
		for _, info := range eng.ProbeOnions() {
			if info.Kind == core.KindSMP && info.Reachable {
				ready = true
			}
		}
		if ready {
			break
		}
		time.Sleep(8 * time.Second)
	}
	fmt.Println("== SMP onion reachable before relay start:", ready, "("+smpAddr0+")")

	fmt.Println("== starting relay (fresh registration via the onion through Tor)")
	if err := eng.StartRelay(); err != nil {
		fmt.Println("StartRelay:", err)
		fmt.Println("== retrying once after 90s (user-style retry)…")
		time.Sleep(90 * time.Second)
		if err := eng.StartRelay(); err != nil {
			fmt.Println("FAIL: StartRelay:", err)
			os.Exit(1)
		}
	}
	smpAddr := core.Address(core.KindSMP, eng.State.Settings(), eng.State.Server(core.KindSMP))
	fmt.Println("== SMP address:", smpAddr)

	start := time.Now()
	deadline := start.Add(4 * time.Minute)
	for time.Now().Before(deadline) {
		time.Sleep(5 * time.Second)
		rec := eng.State.Server(core.KindRelay)
		st := eng.Sv.Status(core.KindRelay)
		fmt.Printf("t=%4ds relay=%v addr=%q\n", int(time.Since(start).Seconds()), st.State == core.ProcRunning, rec.Fingerprint)
		if strings.HasPrefix(rec.Fingerprint, "http") {
			onionOK := strings.Contains(rec.Fingerprint, ".onion")
			fmt.Println("\nRESULT: relay address created:")
			fmt.Println(rec.Fingerprint)
			fmt.Println("registered via onion host:", onionOK)
			if !onionOK {
				fmt.Println("FAIL: address host is not the SMP onion")
				os.Exit(1)
			}
			if verr := core.ValidateRelayAddress(rec.Fingerprint); verr != nil {
				fmt.Println("FAIL: relay address malformed:", verr)
				os.Exit(1)
			}
			fmt.Println("address passes structural validation (the app's parser form)")
			fmt.Println("TOR-FIRST OK")
			os.Exit(0)
		}
	}
	data, _ := os.ReadFile(eng.Paths.ProcLogFile(core.KindRelay))
	fmt.Println("TIMEOUT - no address. relay log:")
	fmt.Println(string(data))
	os.Exit(1)
}

func firstLine(err error) string {
	if err == nil {
		return "<nil>"
	}
	s := err.Error()
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	if len(s) > 140 {
		s = s[:140] + "..."
	}
	return s
}
