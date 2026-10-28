package core

import (
	"fmt"
	"sync"
)

// ReachabilityInfo is the result of probing one server's onion service
// through the managed Tor instance - the same network path a SimpleX app
// on another device would take (SOCKS5 CONNECT through Tor to the onion
// address). A successful probe proves the full chain: Tor is bootstrapped,
// the hidden-service descriptor is published, the rendezvous circuit
// works and the server accepted the connection.
type ReachabilityInfo struct {
	Kind      Kind
	Name      string
	OnionHost string
	// Reachable is true when the connection through Tor succeeded.
	Reachable bool
	// Skipped is a human explanation when the probe was not attempted.
	Skipped string
	// Err is the probe error when !Reachable && Skipped == "".
	Err string
}

// ProbeOnions checks every installed server kind's onion service through
// the managed Tor SOCKS port, in PARALLEL, and returns all results once
// every probe finished. Probing sequentially made the dashboard verdicts
// lag minutes behind reality (each onion rendezvous can take up to 20s).
func (e *Engine) ProbeOnions() []ReachabilityInfo {
	var (
		mu  sync.Mutex
		out []ReachabilityInfo
	)
	wait := e.probeOnionsAsync(func(info ReachabilityInfo) {
		mu.Lock()
		out = append(out, info)
		mu.Unlock()
	})
	wait()
	return out
}

// ProbeOnionsAsync probes every installed server's onion service in
// parallel and calls onResult with each individual verdict AS IT
// COMPLETES (not in any fixed order) - so a slow onion rendezvous does
// not stall the other verdicts. It returns immediately; the probes run
// on their own goroutines. Used by the dashboard for live verdicts.
func (e *Engine) ProbeOnionsAsync(onResult func(ReachabilityInfo)) {
	e.probeOnionsAsync(onResult)
}

// probeOnionsAsync probes every installed server's onion service in
// parallel and calls onResult with each individual verdict AS IT
// COMPLETES (not in any fixed order). The returned wait function blocks
// until all probes have finished.
func (e *Engine) probeOnionsAsync(onResult func(ReachabilityInfo)) (wait func()) {
	s := e.State.Get()
	// probe via the CLIENT SOCKS port: in single-hop mode the service
	// instance has SocksPort 0 and probing through it never connects
	// (the Dashboard would show "not reachable" on a working onion).
	socks := ClientSocksPortOf(e.State)
	torUp := e.Tor.Running()
	var wg sync.WaitGroup
	for _, k := range []Kind{KindSMP, KindXFTP, KindNTF} {
		rec := s.Servers[k]
		if rec == nil || !rec.Installed {
			continue
		}
		wg.Add(1)
		go func(k Kind, rec *ServerRec) {
			defer wg.Done()
			info := ReachabilityInfo{Kind: k, Name: Def(k).Name, OnionHost: rec.OnionHost}
			switch {
			case rec.OnionHost == "":
				info.Skipped = "no onion address yet (Tor not connected)"
			case !torUp:
				info.Skipped = "Tor is not running"
			case socks == 0:
				info.Skipped = "Tor SOCKS port unknown"
			default:
				virtual := rec.Port
				if virtual == 0 {
					virtual = DefaultPort(k)
				}
				if err := probeTorSOCKS(socks, rec.OnionHost, virtual); err != nil {
					info.Err = err.Error()
				} else {
					info.Reachable = true
				}
			}
			onResult(info)
		}(k, rec)
	}
	return wg.Wait
}

// ProbeSummary renders one probe result for logs and the CLI.
func (r ReachabilityInfo) ProbeSummary() string {
	switch {
	case r.Skipped != "":
		return fmt.Sprintf("%s: not checked - %s", r.Name, r.Skipped)
	case r.Reachable:
		return fmt.Sprintf("%s: reachable through the Tor network (%s)", r.Name, r.OnionHost)
	default:
		return fmt.Sprintf("%s: NOT reachable through the Tor network - %s", r.Name, r.Err)
	}
}
