package core

import (
        "bytes"
        "context"
        "fmt"
        "os"
        "os/exec"
        "path/filepath"
        "regexp"
        "strings"
        "time"
)

// ServerTestResult is the outcome of the full protocol test
// (the CLI's `/smp test` command): transport connect, then for SMP
// create/secure/delete a queue; for XFTP create/upload/download/compare
// a small file. It proves the server is fully functional, not just
// reachable.
type ServerTestResult struct {
        Kind Kind
        // Passed is true when the test completed every step.
        Passed bool
        // Step is the failing step ("Connect", "CreateQueue", ...) when !Passed.
        Step string
        // Err is the raw error text reported by the test when !Passed.
        Err string
}

// Summary renders the result for CLI output and logs.
func (r ServerTestResult) Summary() string {
        name := Def(r.Kind).Name
        if r.Passed {
                return fmt.Sprintf("%s: full protocol test passed", name)
        }
        if r.Step != "" {
                return fmt.Sprintf("%s: protocol test failed at %s - %s", name, r.Step, r.Err)
        }
        return fmt.Sprintf("%s: protocol test failed - %s", name, r.Err)
}

// testFailedRe parses the CLI's "<SMP|XFTP> server test failed at <Step>, error: <err>" line.
var testFailedRe = regexp.MustCompile(`(?m)server test failed at\s+(\w+)\s*,\s*error:\s*(.+)$`)

// testPassedRe matches "<SMP|XFTP> server test passed".
var testPassedRe = regexp.MustCompile(`(?m)server test passed`)

// relayTestFailedRe parses "relay test failed at <step>, error: <err>".
var relayTestFailedRe = regexp.MustCompile(`(?m)relay test failed at\s+(\w+)\s*,\s*error:\s*(.+)$`)

// relayTestPassedRe matches "relay test passed".
var relayTestPassedRe = regexp.MustCompile(`(?m)relay test passed`)

// RunServerTest executes the full protocol test against the given server
// kind's own address, through the managed Tor SOCKS port - i.e. the exact
// network path and wire protocol a SimpleX app on another device uses.
//
// The test runs the simplex-chat CLI one-shot (`-e '/smp test ...'`)
// against a dedicated throw-away profile database under the sxops data
// root (created on first use; the display-name prompt is answered via
// stdin). KindRelay tests the relay's https link via `/relay test`.
func (e *Engine) RunServerTest(k Kind) ServerTestResult {
        s := e.State.Get()
        rec := s.Servers[k]
        res := ServerTestResult{Kind: k}
        if rec == nil || !rec.Installed {
                res.Err = "not installed"
                return res
        }

        var address string
        if k == KindRelay {
                // v1.6.6: the relay test also requires a USABLE own SMP
                // server - installed, enabled and running. The test agent
                // is locked to the own servers (-s in runCLIOneShot), so
                // with the SMP server down the test could only fail; and
                // running it anyway would be worse: an unlocked CLI picks
                // its preset (official) servers whenever it creates a
                // connection of its own. There is no such fallback.
                if reason := e.ownSMPDownReason(); reason != "" {
                        res.Err = reason
                        return res
                }
                address = relayAddressOf(rec)
                if address == "" {
                        res.Err = "relay address is not created yet (start the chat relay once)"
                        return res
                }
                // v1.6.1: the relay link is only ever tested when it is
                // hosted on this deployment's own SMP server. A legacy
                // link on an official server is never contacted - sxops
                // does not connect to servers it does not own - and when
                // the own SMP address itself is not found there is
                // nothing to verify against, so the test is skipped
                // rather than falling back to anything else.
                smpAddress := Address(KindSMP, s.Settings, s.Servers[KindSMP])
                if reason := relayLinkTestErr(smpAddress, address); reason != "" {
                        res.Err = reason
                        return res
                }
        } else {
                address = Address(k, s.Settings, rec)
                if address == "" || strings.Contains(address, "YOUR-SERVER-HOST") {
                        res.Err = "no address yet (Tor not connected?)"
                        return res
                }
        }

        if relayTestNeedsTor(k, address) && !e.Tor.Running() {
                res.Err = "Tor is not running - start it first"
                return res
        }

        bin := e.Paths.BinPath(KindRelay) // the chat relay binary IS the simplex-chat CLI
        if _, err := os.Stat(bin); err != nil {
                res.Err = "simplex-chat CLI not installed"
                return res
        }

        cmd := testCommandFor(k)
        out, err := e.runCLIOneShot(bin, cmd+" "+address, 120*time.Second)
        if out == "" && err != nil {
                res.Err = err.Error()
                return res
        }
        parseServerTestOut(k, out, &res)
        if out == "" {
                res.Err = "empty output from the test command"
        }
        return res
}

// relayAddressOf returns the recorded relay https address, if any.
func relayAddressOf(rec *ServerRec) string {
        if rec != nil && strings.HasPrefix(rec.Fingerprint, "http") {
                return rec.Fingerprint
        }
        return ""
}

// relayLinkTestErr returns the reason the recorded relay https link
// must NOT be protocol-tested ("" = it is safe to test). v1.6.1:
// testing a link means connecting to its host, so the host must be this
// deployment's own SMP server - a legacy link on an official server is
// never contacted, and without the own SMP address the test is skipped
// outright (no connection, no fallback, not even a reachability probe).
// The caller decides how the reason is reported; `sxops check` skips
// silently (v1.6.1 behavior, restored in v1.6.4) while the own-SMP
// relay stop guard (StopRelayIfOwnSMPMissing) enforces the rule on the
// RUNNING relay process instead.
func relayLinkTestErr(smpAddress, link string) string {
        if ownSMPNotFound(smpAddress) {
                return "own SMP server not found - sxops never connects to other servers"
        }
        if host, foreign := RelayLinkForeign(smpAddress, link); foreign {
                return fmt.Sprintf("relay link is hosted on %s - not your server (sxops never connects to other servers; replace it via Stop + Reset on the chat relay)", host)
        }
        return ""
}

// relayTestNeedsTor reports whether reaching the tested address needs the
// local Tor SOCKS proxy (onion-only deployments).
func relayTestNeedsTor(k Kind, address string) bool {
        return strings.Contains(address, ".onion")
}

// testCommandFor maps a kind to the CLI test command verb (no trailing
// space: the command parser treats double spaces as empty arguments).
func testCommandFor(k Kind) string {
        switch k {
        case KindSMP:
                return "/smp test"
        case KindXFTP:
                return "/xftp test"
        case KindRelay:
                return "/relay test"
        }
        return "/smp test"
}

// parseServerTestOut fills res from the CLI test output.
func parseServerTestOut(k Kind, out string, res *ServerTestResult) {
        if k == KindRelay {
                switch {
                case relayTestPassedRe.MatchString(out):
                        res.Passed = true
                case relayTestFailedRe.MatchString(out):
                        m := relayTestFailedRe.FindStringSubmatch(out)
                        res.Step, res.Err = m[1], strings.TrimSpace(m[2])
                default:
                        res.Err = firstErrLine(out)
                }
                return
        }
        switch {
        case testPassedRe.MatchString(out):
                res.Passed = true
        case testFailedRe.MatchString(out):
                m := testFailedRe.FindStringSubmatch(out)
                res.Step, res.Err = m[1], strings.TrimSpace(m[2])
        default:
                res.Err = firstErrLine(out)
        }
}

// firstErrLine extracts the first line that looks like an error from
// otherwise unparsed CLI output.
func firstErrLine(out string) string {
        for _, line := range strings.Split(out, "\n") {
                l := strings.TrimSpace(line)
                if l == "" || strings.HasPrefix(l, "display name") || strings.HasPrefix(l, "sxops-") {
                        continue
                }
                if strings.Contains(strings.ToLower(l), "error") || strings.Contains(l, "failed") {
                        return l
                }
        }
        return "unexpected test output"
}

// runCLIOneShot runs the simplex-chat CLI with a single `-e` command
// against a fresh throw-away profile, feeding the display-name prompt
// on first use. It returns combined stdout+stderr.
//
// v1.6.6: the run is LOCKED to the own SMP server via -s, exactly like
// every other sxops CLI invocation - without it the test agent creates
// its own connections on the CLI's preset (official) servers whenever
// it picks a server itself. The profile is throw-away PER RUN: the
// pre-1.6.6 persistent self-test profile may hold a user registered
// back when the test ran unlocked, so it is removed here once and never
// used again.
func (e *Engine) runCLIOneShot(bin, command string, timeout time.Duration) (string, error) {
        if err := os.MkdirAll(e.Paths.TestDir(), 0o700); err != nil {
                return "", err
        }
        // legacy persistent self-test profile (pre--s-lock era): drop it
        _ = os.RemoveAll(e.Paths.TestDBPrefix())
        tmp, err := os.MkdirTemp(e.Paths.TestDir(), "run-")
        if err != nil {
                return "", err
        }
        defer os.RemoveAll(tmp)
        args := []string{"-d", filepath.Join(tmp, "servertest")}
        // v1.6.6: lock every test connection to the own SMP server.
        if smpAddress := Address(KindSMP, e.State.Settings(), e.State.Server(KindSMP));
                smpAddress != "" && !strings.Contains(smpAddress, "YOUR-SERVER-HOST") {
                args = append(args, "-s", smpAddress)
        }
        // route through the CLIENT SOCKS port: in single-hop mode the
        // service instance runs with SocksPort 0 and the old code left the
        // one-shot CLI without any proxy - onion tests then failed.
        if socks := ClientSocksPortOf(e.State); socks > 0 {
                args = append(args, "--socks-proxy", "127.0.0.1:"+fmt.Sprint(socks))
        }
        // slow Tor rendezvous must not abort the test with the CLI's short default
        args = append(args, "--tcp-timeout", "45")
        args = append(args, "-e", command)

        ctx, cancel := context.WithTimeout(context.Background(), timeout)
        defer cancel()
        cmd := exec.CommandContext(ctx, bin, args...)
        cmd.Env = os.Environ()
        var outb bytes.Buffer
        cmd.Stdout = &outb
        cmd.Stderr = &outb
        // first run prompts "display name: " - answer it via stdin
        cmd.Stdin = strings.NewReader("sxops self-test\n")
        _ = cmd.Run() // exit code is not meaningful for the CLI; output is parsed
        out := outb.String()
        // keep the last test output for diagnostics (shown in the Logs view)
        _ = os.WriteFile(filepath.Join(e.Paths.TestDir(), "last-test.txt"),
                []byte(fmt.Sprintf("argv: %q\n%s", args, out)), 0o600)
        return out, ctx.Err()
}
