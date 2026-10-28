package core

import (
        "fmt"
        "os"
        "path/filepath"
        "regexp"
        "strconv"
        "strings"
        "time"
)

// RelayPlan is the decided registration path for the chat relay's
// address. The relay creates its https link exactly ONCE (first start)
// by registering a queue on an SMP server (--relay-address-server) and
// never retries a failed registration - so sxops must guarantee Tor is
// running and the registration server is reachable at that moment.
//
// The link embeds the host of the registration server
// (https://<host>/r#key?p=<port>&c=<smp-fingerprint>), so the choice is
// permanent for the deployment: sxops deployments register via the
// onion address through the managed Tor SOCKS port (the engine makes
// sure Tor is running BEFORE the relay starts - there is no direct
// fallback); public-host deployments register via the public host.
type RelayPlan struct {
        // Register is the SMP address passed to --relay-address-server
        // ("" = not registering on an own server).
        Register string
        // XFTP is the own XFTP server address passed to --xftp-server so
        // the relay's own file uploads also stay on this deployment
        // ("" = no own XFTP server - the flag is omitted).
        XFTP string
        // Socks is the Tor SOCKS port to route the connection through
        // (0 = no SOCKS proxy).
        Socks int
}

// relayArgsWith builds the relay argv from a decided plan.
func relayArgsWith(s Settings, paths *Paths, plan RelayPlan) []string {
        args := []string{"--relay", "--headless", "--user-display-name", s.RelayName}
        // Tor circuits can take a while to build on slow networks; the
        // CLI's default TCP timeout aborts the broker connection with
        // NETimeout before the rendezvous completes.
        args = append(args, "--tcp-timeout", "45")
        if s.RelayImage != "" {
                args = append(args, "--user-image-file", s.RelayImage)
        }
        // v1.6.0: -s REPLACES the CLI's whole server list with the own
        // SMP server for this run (simplex-chat's --server option) - the
        // profile's preset/official servers are ignored, so EVERY new
        // queue or connection the relay creates lands on the own server.
        // Without it the CLI would fall back to its preset list (the
        // official SimpleX servers) whenever it picks a server itself.
        // --relay-address-server additionally pins the one-shot relay
        // address registration to the same server.
        if plan.Register != "" {
                args = append(args, "-s", plan.Register)
                args = append(args, "--relay-address-server", plan.Register)
        }
        if plan.XFTP != "" && !strings.Contains(plan.XFTP, "YOUR-SERVER-HOST") {
                // same principle for files the relay sends itself: upload
                // via the own XFTP server, never the official ones
                args = append(args, "--xftp-server", plan.XFTP)
        }
        if plan.Register != "" && strings.Contains(plan.Register, ".onion") {
                // reaching an onion SMP server requires the local Tor SOCKS proxy
                if plan.Socks > 0 {
                        args = append(args, "--socks-proxy", "127.0.0.1:"+strconv.Itoa(plan.Socks))
                } else {
                        args = append(args, "-x") // default 9050
                }
        }
        if s.RelayWebEnabled && s.RelayWebDomain != "" {
                args = append(args,
                        "--relay-web-domain", s.RelayWebDomain,
                        "--relay-web-dir", filepath.Join(paths.RelayDir(), "web", "channel"),
                        "--relay-web-cors-file", filepath.Join(paths.RelayDir(), "web", "cors.conf"),
                )
        }
        args = append(args, "-d", paths.RelayDBPrefix())
        return args
}

// RelayArgs builds the command line for the chat relay
// (simplex-chat --relay --headless ...). The registration path is
// decided synchronously without probing: onion deployments go through
// the managed Tor SOCKS port (torSocks, 0 = unknown -> -x default).
//
// The engine's startRelay makes the smarter, probing decision
// (see RelayPlan); this variant is used by one-shot callers.
func RelayArgs(s Settings, paths *Paths, smpAddress string, torSocks int) []string {
        return relayArgsWith(s, paths, relayPlanSync(s, smpAddress, torSocks))
}

// relayPlanSync picks the registration path without probing the network:
//
//   - onion deployments register through the managed Tor SOCKS port
//   - public hosts are reachable without Tor
//
// Either way the engine guarantees Tor is running before the relay
// starts when the registration target needs it (ensureTorRunning).
// The relay always registers on the own SMP server - a sxops
// deployment never depends on the official SimpleX servers - and
// v1.6.0 additionally LOCKED it to the own server (-s), so no new
// connection of its can ever reach another server.
func relayPlanSync(s Settings, smpAddress string, torSocks int) RelayPlan {
        if smpAddress == "" {
                return RelayPlan{}
        }
        if strings.Contains(smpAddress, ".onion") {
                return RelayPlan{Register: smpAddress, Socks: torSocks}
        }
        // public host: reachable without Tor
        return RelayPlan{Register: smpAddress}
}

// relaySocksOfPlan returns the Tor SOCKS port a relay started with
// this plan actually uses: the explicit --socks-proxy port, the
// CLI's -x default 9050 when the registration is an onion without
// an explicit port, or -1 when the argv carries no proxy at all
// (public-host registration). The engine records it in
// TorRec.RelaySocks so the Statuses watchdog can detect drift.
func relaySocksOfPlan(plan RelayPlan) int {
        if !strings.Contains(plan.Register, ".onion") {
                return -1
        }
        if plan.Socks > 0 {
                return plan.Socks
        }
        return 9050 // -x default
}

// relaySocksDrifted reports whether a RUNNING relay's recorded SOCKS
// port no longer matches the effective client port. False when the
// registration is on a public host (no proxy needed), when nothing
// was recorded (adopted relay) or when the current port is unknown
// (Tor not up in this state yet) - those cases must not trigger the
// restart watchdog (a public-host relay would otherwise flap
// forever: recorded -1 never equals a real port).
func relaySocksDrifted(smpAddress string, recorded, want int) bool {
        if !strings.Contains(smpAddress, ".onion") {
                return false
        }
        if recorded == 0 || recorded < 0 || want == 0 {
                return false
        }
        return recorded != want
}

// RelayLinkHost extracts the bare host from a relay https link
// ("https://host/r#key?..." -> "host"; port and path stripped).
func RelayLinkHost(link string) string {
        l := strings.TrimSpace(link)
        if i := strings.Index(l, "://"); i >= 0 {
                l = l[i+3:]
        }
        if i := strings.IndexAny(l, "/#?"); i >= 0 {
                l = l[:i]
        }
        if i := strings.LastIndex(l, ":"); i >= 0 {
                l = l[:i]
        }
        return l
}

// smpHostsOf lists the hosts an SMP address advertises
// ("smp://fp@pub.example.com,abc.onion" -> both hosts).
func smpHostsOf(smpAddress string) []string {
        i := strings.LastIndex(smpAddress, "@")
        if i < 0 {
                return nil
        }
        rest := smpAddress[i+1:]
        if j := strings.IndexAny(rest, "/#? "); j >= 0 {
                rest = rest[:j]
        }
        var hosts []string
        for _, h := range strings.Split(rest, ",") {
                if h = strings.TrimSpace(h); h != "" {
                        hosts = append(hosts, h)
                }
        }
        return hosts
}

// RelayLinkForeign reports whether a relay https link is hosted on a
// server that is NOT part of this deployment - e.g. on an official
// SimpleX server, which happens when the link was registered by an
// older sxops that did not force the own SMP server. Such links resolve
// through (and depend on) the official infrastructure and die when
// those servers get retired. Returns the foreign host. An empty or
// placeholder own address never counts as foreign (nothing to compare
// against - no migration on a half-configured system).
func RelayLinkForeign(smpAddress, link string) (string, bool) {
        if smpAddress == "" || strings.Contains(smpAddress, "YOUR-SERVER-HOST") {
                return "", false
        }
        if !strings.HasPrefix(link, "http") {
                return "", false
        }
        h := RelayLinkHost(link)
        if h == "" {
                return "", false
        }
        for _, own := range smpHostsOf(smpAddress) {
                if strings.EqualFold(h, own) {
                        return "", false
                }
        }
        return h, true
}

// relayAddressRe matches the address the CLI prints on first start.
var relayAddressRe = regexp.MustCompile(`(?m)address is created:\s*\n?(https?://\S+)`)

// ExtractRelayAddress scans relay process output for the created address.
func ExtractRelayAddress(output string) string {
        if m := relayAddressRe.FindStringSubmatch(output); m != nil {
                return m[1]
        }
        return ""
}

// ValidateRelayAddress checks that a relay https link has the structure
// the SimpleX app's parser expects. The relay link is a ConnShortLink:
//
//      https://<server-host>/<type>#<key>?h=<hosts>&p=<port>&c=<fingerprint>
//
// with <type> a single character marking the link kind (r = chat relay,
// a = contact, c = channel, g = group, i = invitation) - the ?query is
// optional and follows the #fragment (SimpleX's own ordering). The older
// full form <scheme>://<host>/contact#/?v=..&smp=.. and the legacy
// https://<host>/<b64>#<b64> links are accepted too.
func ValidateRelayAddress(addr string) error {
        addr = strings.TrimSpace(addr)
        if addr == "" {
                return fmt.Errorf("empty relay address")
        }
        if !strings.HasPrefix(addr, "https://") && !strings.HasPrefix(addr, "http://") {
                return fmt.Errorf("relay address does not start with https:// : %s", truncate(addr, 80))
        }
        if strings.ContainsAny(addr, " \t\r\n") {
                return fmt.Errorf("relay address contains whitespace")
        }
        rest := addr[len("https://"):] // http(s) prefix
        host := rest
        if i := strings.IndexAny(rest, "/#?"); i >= 0 {
                host = rest[:i]
        }
        if host == "" || strings.ContainsAny(host, "/#? ") {
                return fmt.Errorf("relay link has no server host")
        }
        // short-link form: /<type-char>#<key>[?query]
        if i := strings.Index(rest, "/"); i > 0 && len(rest) > i+2 && rest[i+2] == '#' {
                t := rest[i+1]
                if !(t >= 'a' && t <= 'z' || t >= 'A' && t <= 'Z') {
                        return fmt.Errorf("relay link type marker %q is not a letter", t)
                }
                key := rest[i+3:]
                if j := strings.IndexByte(key, '?'); j >= 0 {
                        key = key[:j]
                }
                if key == "" {
                        return fmt.Errorf("relay link has an empty #key")
                }
                if strings.ContainsAny(key, "/?#&") {
                        return fmt.Errorf("relay link key contains separator characters")
                }
                return nil
        }
        // full contact-uri form: /contact#/?v=..&smp=.. (or /invitation#/?.)
        if i := strings.Index(rest, "#/?"); i > 0 {
                tail := rest[i+len("#/?"):]
                if !strings.Contains(tail, "smp=") {
                        return fmt.Errorf("full-form relay link has no smp= parameter")
                }
                if !strings.Contains(tail, "v=") {
                        return fmt.Errorf("full-form relay link has no v= parameter")
                }
                return nil
        }
        // legacy form: https://<host>/<b64>#<b64> (the fragment runs to the
        // end with no query)
        if i := strings.IndexByte(rest, '#'); i > 0 {
                tail := rest[i+1:]
                if tail == "" {
                        return fmt.Errorf("legacy relay link has an empty fragment")
                }
                if strings.ContainsAny(tail, "?&/") {
                        return fmt.Errorf("legacy relay link fragment looks malformed")
                }
                return nil
        }
        return fmt.Errorf("relay link has neither #key (short link), #/? (full form) nor #fragment (legacy) form")
}

// RelayAddress reads the recorded relay address from state or the relay log.
func RelayAddress(paths *Paths) (string, error) {
        data, err := os.ReadFile(paths.ProcLogFile(KindRelay))
        if err != nil {
                return "", err
        }
        if addr := ExtractRelayAddress(string(data)); addr != "" {
                return addr, nil
        }
        return "", fmt.Errorf("relay address not created yet (it appears on first start)")
}

// relaySAddressRe parses the `/sa` output: "Your chat address:" followed
// by the https link on the next line.
var relaySAddressRe = regexp.MustCompile(`(?m)Your chat address:\s*\n?(https?://\S+)`)

// oneShotRelayArgs builds the argv for a one-shot CLI run against the
// relay profile DB (`-e` command). v1.6.0: like the long-running relay,
// a one-shot run is LOCKED to the own SMP server via -s - the CLI's
// preset (official) servers are never consulted, whatever the command
// triggers under the hood.
// v1.6.1: callers guarantee a usable own SMP address - a run without
// one is refused before the CLI is ever executed (no fallback), see
// ownSMPNotFound.
func oneShotRelayArgs(dbPrefix, smpAddress string, socks int, command string) []string {
        args := []string{"-d", dbPrefix}
        if smpAddress != "" && !strings.Contains(smpAddress, "YOUR-SERVER-HOST") {
                args = append(args, "-s", smpAddress)
        }
        // reach onion servers via local Tor SOCKS when needed (v1.5.9: the
        // relay always uses the own SMP server, so onion routing is the norm)
        if socks > 0 {
                args = append(args, "--socks-proxy", "127.0.0.1:"+strconv.Itoa(socks))
        } else {
                args = append(args, "-x")
        }
        args = append(args, "-e", command)
        return args
}

// ownSMPNotFound reports whether the own SMP server address is absent
// or still the placeholder - i.e. there is nothing to lock a relay CLI
// run to. v1.6.1: sxops NEVER runs the chat relay (or any one-shot
// command against its profile) in that state: without -s the CLI would
// fall back to its preset list of official SimpleX servers, and there
// must be no such fallback, ever.
func ownSMPNotFound(smpAddress string) bool {
        return smpAddress == "" || strings.Contains(smpAddress, "YOUR-SERVER-HOST")
}

// RelayAddressFromDB recovers the relay address from its profile database
// by running the CLI one-shot `/sa` (show address) against the relay DB.
//
// The relay prints "Chat relay address is created:" exactly ONCE - on the
// first start, when the address is created. sxops watches for it, but if
// that single run was missed (slow Tor, crash, log truncated on restart)
// the address is still in the relay DB and `/sa` re-prints it. It must
// only run while the relay is STOPPED (SQLite single-writer).
//
// smpAddress is the own SMP server address used to lock the run (-s);
// v1.6.1: the run is REFUSED when it is not available - the relay never
// runs without being locked to the own server (no official fallback).
func RelayAddressFromDB(paths *Paths, smpAddress string, socks int) (string, error) {
        if ownSMPNotFound(smpAddress) {
                return "", fmt.Errorf("own SMP server not found - the relay CLI only runs locked to it (start the SMP server first; no fallback to official servers)")
        }
        bin := paths.BinPath(KindRelay)
        if _, err := os.Stat(bin); err != nil {
                return "", fmt.Errorf("simplex-chat CLI not installed")
        }
        args := oneShotRelayArgs(paths.RelayDBPrefix(), smpAddress, socks, "/sa")
        out, err := CommandOutput(bin, args, nil, 60*time.Second)
        if m := relaySAddressRe.FindStringSubmatch(out); m != nil {
                return m[1], nil
        }
        detail := strings.TrimSpace(out)
        if detail == "" && err != nil {
                return "", err
        }
        if detail == "" {
                detail = "no address in the relay profile yet"
        }
        return "", fmt.Errorf("relay address not recovered: %s", detail)
}

// RelayCommand runs a one-off relay command via `-e`
// (e.g. "/set profile image file /path/avatar.png"). v1.6.0: the run is
// locked to the own SMP server (-s), like every sxops-managed CLI run.
// v1.6.1: refused outright when the own SMP server is not found - the
// command never runs unlocked against official servers.
func RelayCommand(paths *Paths, state *StateStore, command string) (string, error) {
        s := state.Get()
        smpAddress := Address(KindSMP, s.Settings, s.Servers[KindSMP])
        if ownSMPNotFound(smpAddress) {
                return "", fmt.Errorf("own SMP server not found - the relay CLI only runs locked to it (start the SMP server first; no fallback to official servers)")
        }
        bin := paths.BinPath(KindRelay)
        if _, err := os.Stat(bin); err != nil {
                return "", fmt.Errorf("simplex-chat binary not installed")
        }
        args := oneShotRelayArgs(paths.RelayDBPrefix(), smpAddress, ClientSocksPortOf(state), command)
        out, err := CommandOutput(bin, args, nil, 0)
        return out, err
}
