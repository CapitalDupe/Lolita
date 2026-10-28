// E2E harness: verify single-hop mode (v1.5.4) - the dual Tor instance
// architecture.
//
//  1. Enable single-hop via the engine API (HandleSingleHopChange) - the
//     hidden services must be regenerated (Tor forbids re-using keys).
//  2. Start Tor: BOTH instances must come up (service with SocksPort 0 +
//     client-only with its own SOCKS port).
//  3. The fresh onion must publish and answer through the CLIENT
//     instance's SOCKS port.
package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"sxops/internal/core"
)

func main() {
	eng, err := core.NewEngine(core.NopInstallLog())
	if err != nil {
		panic(err)
	}

	fmt.Println("== stopping everything (incl. Tor)")
	eng.StopAll(true)
	for i := 0; i < 15 && eng.Tor.Running(); i++ {
		time.Sleep(time.Second)
		eng.StopAll(true)
	}

	// 1. enable single-hop through the settings flow
	s := eng.State.Settings()
	s.SingleHopOnion = false
	_ = eng.State.SetSettings(s)
	s.SingleHopOnion = true
	if err := eng.State.SetSettings(s); err != nil {
		panic(err)
	}
	warn, err := eng.HandleSingleHopChange()
	if err != nil {
		fmt.Println("FAIL: HandleSingleHopChange:", err)
		os.Exit(1)
	}
	fmt.Println("== single-hop change applied; warning returned:", warn != "")

	// the torrc must have SocksPort 0 + the single-hop flags, and a
	// client torrc must exist
	torrc, _ := os.ReadFile(eng.Paths.TorDir() + "/torrc")
	torrcStr := string(torrc)
	need(torrcStr, "SocksPort 0", "service torrc must disable the SOCKS port")
	need(torrcStr, "HiddenServiceSingleHopMode 1", "service torrc must enable single-hop")
	need(torrcStr, "HiddenServiceNonAnonymousMode 1", "service torrc must enable non-anonymous mode")
	if _, err := os.Stat(eng.Paths.TorClientRcPath()); err != nil {
		fmt.Println("FAIL: client torrc missing after enabling single-hop")
		os.Exit(1)
	}
	clientRc, _ := os.ReadFile(eng.Paths.TorClientRcPath())
	need(string(clientRc), "SocksPort 127.0.0.1:", "client torrc must have its own SOCKS port")
	if strings.Contains(string(clientRc), "HiddenServiceDir") {
		fmt.Println("FAIL: client torrc must NOT host hidden services")
		os.Exit(1)
	}
	fmt.Println("== torrc pair generated correctly (service: SocksPort 0 + single-hop; client: own SOCKS)")

	// 2. start Tor (both instances)
	fmt.Println("== starting Tor (single-hop mode)")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	if err := eng.Tor.Start(ctx); err != nil {
		fmt.Println("FAIL: Tor start:", err)
		os.Exit(1)
	}
	st := eng.State.Get().Tor
	fmt.Printf("== service: socks=%d ctrl=%d | client: socks=%d ctrl=%d\n",
		st.SocksPort, st.ControlPort, st.ClientSocksPort, st.ClientControlPort)
	if st.ClientSocksPort == 0 {
		fmt.Println("FAIL: client instance ports not recorded")
		os.Exit(1)
	}
	if got := core.ClientSocksPortOf(eng.State); got != st.ClientSocksPort {
		fmt.Println("FAIL: ClientSocksPortOf mismatch:", got)
		os.Exit(1)
	}
	fmt.Println("== ClientSocksPortOf() returns the client port in single-hop mode")

	// the onion service forwards to the local SMP port - the server must
	// be running for the probe to answer
	fmt.Println("== starting the SMP server (behind the single-hop onion)")
	if err := eng.StartComponent(core.KindSMP); err != nil {
		fmt.Println("FAIL: StartComponent(SMP):", err)
		os.Exit(1)
	}

	// 3. wait for the fresh onion to publish and answer through the
	// CLIENT SOCKS port (the reachability probes use exactly this path)
	fmt.Println("== waiting for the fresh SMP onion to answer through the client instance")
	deadline := time.Now().Add(6 * time.Minute)
	ok := false
	for time.Now().Before(deadline) {
		for _, info := range eng.ProbeOnions() {
			if info.Kind == core.KindSMP && info.Reachable {
				ok = true
			}
		}
		if ok {
			break
		}
		time.Sleep(8 * time.Second)
	}
	if !ok {
		fmt.Println("FAIL: SMP onion did not become reachable through the client instance")
		os.Exit(1)
	}
	rec := eng.State.Server(core.KindSMP)
	fmt.Println("== fresh onion:", rec.OnionHost)
	fmt.Println("SINGLE-HOP OK: dual-instance architecture verified")
}

func need(s, sub, msg string) {
	if !strings.Contains(s, sub) {
		fmt.Printf("FAIL: %s (torrc lacks %q)\n", msg, sub)
		os.Exit(1)
	}
}
