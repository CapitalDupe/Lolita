// E2E harness: v1.5.6 relay "Wait response" timeout fixes.
//
// Usage: v156harness <mode>
//
//	p0to4  - bootstrap, SMP test via client SOCKS, fresh relay
//	        registration, /relay test round-trip, SOCKS-drift watchdog
//	toggle - single-hop OFF: relay wiped, Tor restarted, new onion,
//	        wait (bounded) for the automatic re-registration
//	final  - start the relay (the delayed-start path once the onion is
//	        live) and run the final /relay test
//
// The sandbox reaps background processes at tool-call boundaries, so
// each mode runs in the foreground of its own call; sxops' detached
// children (Tor, servers, relay) survive between them.
package main

import (
	"fmt"
	"os"
	"strings"
	"time"

	"sxops/internal/core"
)

func main() {
	mode := "p0to4"
	if len(os.Args) > 1 {
		mode = os.Args[1]
	}
	eng, err := core.NewEngine(core.NopInstallLog())
	if err != nil {
		panic(err)
	}
	switch mode {
	case "p0to4":
		p0to4(eng)
	case "toggle":
		toggle(eng)
	case "final":
		finalCheck(eng)
	default:
		fmt.Println("unknown mode:", mode)
		os.Exit(2)
	}
}

func p0to4(eng *core.Engine) {
	// ---- Phase 0: bootstrap the live environment state ----
	fmt.Println("== P0: bootstrapping state from the live single-hop environment")
	if h, err := eng.Backend.ReadOnionHost(core.KindSMP); err == nil && h != "" {
		_ = eng.Provisioner.SetOnionHost(core.KindSMP, h)
		fmt.Println("   SMP onion:", h)
	}
	s := eng.State.Settings()
	if !s.SingleHopOnion {
		fmt.Println("SKIP: environment is not in single-hop mode")
		os.Exit(0)
	}
	csocks := core.ClientSocksPortOf(eng.State)
	fmt.Printf("   client SOCKS port: %d (service SOCKS in state: %d)\n", csocks, eng.State.Get().Tor.SocksPort)
	if csocks == 0 || !eng.Tor.Running() {
		fmt.Println("FAIL: Tor not running or client port unknown")
		os.Exit(1)
	}

	// ---- Phase 1: SMP protocol test through the CLIENT port ----
	fmt.Println("== P1: SMP full protocol test (must go through the client SOCKS port)")
	if r := eng.RunServerTest(core.KindSMP); !r.Passed {
		fmt.Println("FAIL: SMP test:", r.Summary())
		dumpLastTest(eng)
		os.Exit(1)
	}
	fmt.Println("    SMP full protocol test passed")

	// ---- Phase 2: fresh relay registration through the onion ----
	fmt.Println("== P2: ResetRelay -> fresh registration via the SMP onion")
	fmt.Println("    old link:", eng.State.Server(core.KindRelay).Fingerprint)
	if err := eng.ResetRelay(); err != nil {
		fmt.Println("FAIL: ResetRelay:", err)
		os.Exit(1)
	}
	link := waitLink(eng, 4*time.Minute)
	if link == "" {
		fmt.Println("FAIL: no relay address created in 4 minutes")
		os.Exit(1)
	}
	if !strings.Contains(link, ".onion") {
		fmt.Println("FAIL: new relay link has no onion host:", link)
		os.Exit(1)
	}
	recorded := eng.State.Get().Tor.RelaySocks
	fmt.Println("    new link:", link)
	fmt.Printf("    RelaySocks recorded: %d (client port: %d)\n", recorded, csocks)
	if recorded != csocks {
		fmt.Println("FAIL: RelaySocks != client SOCKS port")
		os.Exit(1)
	}

	// ---- Phase 3: the app's relay test, same wire path ----
	fmt.Println("== P3: /relay test (the app's Test button protocol)")
	if r := eng.RunServerTest(core.KindRelay); !r.Passed {
		fmt.Println("FAIL: relay test:", r.Summary())
		dumpLastTest(eng)
		os.Exit(1)
	}
	fmt.Println("    relay test passed")

	// ---- Phase 4: SOCKS-drift watchdog ----
	fmt.Println("== P4: watchdog - corrupt the recorded port, expect an automatic restart")
	dead := csocks + 7
	if err := eng.State.With(func(st *core.State) { st.Tor.RelaySocks = dead }); err != nil {
		panic(err)
	}
	eng.Statuses() // triggers watchRelaySocksDrift
	deadline := time.Now().Add(90 * time.Second)
	healed := false
	for time.Now().Before(deadline) {
		if eng.State.Get().Tor.RelaySocks == csocks && eng.Sv.Status(core.KindRelay).State == core.ProcRunning {
			healed = true
			break
		}
		time.Sleep(2 * time.Second)
	}
	if !healed {
		fmt.Printf("FAIL: watchdog did not restart the relay on port %d (recorded %d)\n", csocks, eng.State.Get().Tor.RelaySocks)
		os.Exit(1)
	}
	fmt.Println("    watchdog restarted the relay on the current port")
	if r := eng.RunServerTest(core.KindRelay); !r.Passed {
		fmt.Println("FAIL: relay test after watchdog restart:", r.Summary())
		dumpLastTest(eng)
		os.Exit(1)
	}
	fmt.Println("    relay test still passes after the restart")
	fmt.Println("P0-P4 OK")
}

func toggle(eng *core.Engine) {
	fmt.Println("== P5: single-hop toggle OFF (relay reset + Tor restart + re-registration)")
	oldOnion := eng.State.Server(core.KindSMP).OnionHost
	s := eng.State.Settings()
	if !s.SingleHopOnion {
		// repair a half-done toggle from an aborted run: the on-disk
		// torrc/keys are still single-hop, so restore the setting first
		fmt.Println("    (repairing a half-done toggle: restoring single-hop)")
		s.SingleHopOnion = true
		if err := eng.State.SetSettings(s); err != nil {
			panic(err)
		}
	}
	s.SingleHopOnion = false
	if err := eng.State.SetSettings(s); err != nil {
		panic(err)
	}
	if _, err := eng.HandleSingleHopChange(); err != nil {
		fmt.Println("FAIL: HandleSingleHopChange:", err)
		os.Exit(1)
	}
	if fp := eng.State.Server(core.KindRelay).Fingerprint; fp != "" {
		fmt.Println("FAIL: relay link not wiped:", fp)
		os.Exit(1)
	}
	if !eng.Tor.Running() {
		fmt.Println("FAIL: Tor not restarted by HandleSingleHopChange")
		os.Exit(1)
	}
	newOnion := eng.State.Server(core.KindSMP).OnionHost
	fmt.Printf("    onion %s -> %s\n", oldOnion, newOnion)
	if newOnion == "" || newOnion == oldOnion {
		fmt.Println("FAIL: onion host not regenerated")
		os.Exit(1)
	}
	if eng.State.Get().Tor.RelaySocks != 0 {
		fmt.Println("FAIL: RelaySocks not cleared")
		os.Exit(1)
	}
	// give the automatic re-registration a bounded window inside this run
	fmt.Println("    waiting for the automatic relay re-registration (bounded window)...")
	link := waitLink(eng, 4*time.Minute)
	if link != "" {
		fmt.Println("    new link (auto):", link)
		if r := eng.RunServerTest(core.KindRelay); !r.Passed {
			fmt.Println("FAIL: relay test after toggle:", r.Summary())
			dumpLastTest(eng)
			os.Exit(1)
		}
		fmt.Println("TOGGLE OK: re-registered and relay test passed inside the window")
		return
	}
	fmt.Println("TOGGLE PARTIAL: relay not re-registered within this run's window (descriptor spread) - run mode=final later")
}

func finalCheck(eng *core.Engine) {
	fmt.Println("== FINAL: delayed relay start + relay test")
	link := eng.State.Server(core.KindRelay).Fingerprint
	if !strings.HasPrefix(link, "https") {
		fmt.Println("    no link yet - starting the relay (the delayed-start path)")
		if err := eng.StartRelay(); err != nil {
			fmt.Println("FAIL: StartRelay:", err)
			os.Exit(1)
		}
		link = waitLink(eng, 4*time.Minute)
		if link == "" {
			fmt.Println("FAIL: relay address not created")
			os.Exit(1)
		}
	}
	fmt.Println("    link:", link)
	if r := eng.RunServerTest(core.KindRelay); !r.Passed {
		fmt.Println("FAIL: final relay test:", r.Summary())
		dumpLastTest(eng)
		os.Exit(1)
	}
	fmt.Println("FINAL OK: relay test passed after the single-hop toggle")
}

func waitLink(eng *core.Engine, wait time.Duration) string {
	deadline := time.Now().Add(wait)
	for time.Now().Before(deadline) {
		if fp := eng.State.Server(core.KindRelay).Fingerprint; strings.HasPrefix(fp, "https") {
			return fp
		}
		time.Sleep(3 * time.Second)
	}
	return ""
}

func dumpLastTest(eng *core.Engine) {
	data, err := os.ReadFile(eng.Paths.TestDir() + "/last-test.txt")
	if err == nil {
		fmt.Println("---- last-test.txt ----")
		fmt.Println(string(data))
	}
}
