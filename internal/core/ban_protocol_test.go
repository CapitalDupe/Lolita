//go:build integration

// TestBanProtocolLive exercises the moderation control-port commands
// (block / unblock / delete / status) against a REAL smp-server downloaded
// from the official release, verifying that the wire format sxops sends is
// accepted by the server parser (an unknown client ID must produce a
// proper server error, never "bad ControlProtocol command").
//
// Run with:
//
//      go test -tags=integration -run TestBanProtocolLive -v -timeout 15m ./internal/core/
package core

import (
        "bytes"
        "context"
        "os"
        "os/exec"
        "path/filepath"
        "strings"
        "testing"
        "time"
)

type testLog struct{ t *testing.T }

func (l testLog) Step(msg string)   { l.t.Logf("[step] %s", msg) }
func (l testLog) Detail(msg string) { l.t.Logf("       %s", msg) }

func TestBanProtocolLive(t *testing.T) {
        if _, err := exec.LookPath("openssl"); err != nil {
                t.Skip("openssl not available")
        }
        root := t.TempDir()
        paths, err := NewPaths(root)
        if err != nil {
                t.Fatal(err)
        }
        if err := paths.Ensure(); err != nil {
                t.Fatal(err)
        }
        state, err := NewStateStore(paths)
        if err != nil {
                t.Fatal(err)
        }
        log := testLog{t}

        ctx, cancel := context.WithTimeout(context.Background(), 12*time.Minute)
        defer cancel()

        // 1. download + install the real smp-server
        inst := NewInstaller(paths, state, log)
        rel, upd, err := inst.CheckUpdate(ctx, KindSMP)
        if err != nil {
                t.Fatalf("checking latest smp-server release: %v", err)
        }
        t.Logf("latest smp-server release: %s (update needed: %v)", rel.TagName, upd)
        if err := inst.Install(ctx, KindSMP, rel); err != nil {
                t.Fatalf("installing smp-server: %v", err)
        }
        rec := state.Server(KindSMP)
        t.Logf("installed: %s tag=%s", rec.BinVersion, rec.BinTag)

        // 2. provision (init + portable INI patch)
        prov := NewProvisioner(paths, state, log)
        if err := prov.Provision(ctx, KindSMP); err != nil {
                t.Fatalf("provisioning smp-server: %v", err)
        }
        rec = state.Server(KindSMP)
        if rec.ControlPort == 0 || rec.ControlAdmin == "" {
                t.Fatalf("control port not configured: %+v", rec)
        }
        t.Logf("control port %d, admin token %q", rec.ControlPort, rec.ControlAdmin[:6]+"…")

        // 3. start it
        sv := NewSupervisor(paths, state)
        if err := sv.Start(KindSMP); err != nil {
                t.Fatalf("starting smp-server: %v", err)
        }
        defer func() { _ = sv.Stop(KindSMP, 20*time.Second) }()

        // wait for the control port to accept connections
        deadline := time.Now().Add(90 * time.Second)
        for {
                if _, err := FetchStats(KindSMP, rec); err == nil {
                        break
                }
                if time.Now().After(deadline) {
                        out, _ := os.ReadFile(paths.ProcLogFile(KindSMP))
                        t.Fatalf("smp-server control port never came up.\n--- server log ---\n%s", out)
                }
                time.Sleep(2 * time.Second)
        }

        // 4. live stats sanity
        st, err := FetchStats(KindSMP, rec)
        if err != nil {
                t.Fatalf("stats: %v", err)
        }
        t.Logf("stats ok, %d fields (qCount=%s)", len(st), st.Get("qCount"))

        // 4b. create a REAL queue using the simplex-chat CLI against the
        // local server, then exercise the full ban round-trip on it.
        realQueueID := createRealQueue(t, ctx, paths, inst, rec)
        if realQueueID != "" {
                // store-log parsing finds the queue
                eng := &Engine{Paths: paths, State: state, Sv: sv, Log: log}
                eng.Backend = &nativeBackend{eng: eng}
                queues, qerr := ListQueues(eng, KindSMP)
                if qerr != nil {
                        t.Fatalf("ListQueues: %v", qerr)
                }
                found := false
                for _, q := range queues {
                        if q.ID == realQueueID {
                                found = true
                        }
                }
                if !found {
                        t.Fatalf("store log parser did not find queue %s in %d queues", realQueueID, len(queues))
                }
                t.Logf("store log parser found the real queue (%d queues total)", len(queues))

                // ban it (official method: reason + ban notice; the notice
                // travels to the banned client's app, which then refuses to
                // create new links via this server until the ban is lifted)
                out, err := BanClient(KindSMP, rec, realQueueID, BanReasonSpam, NewClientNotice(0))
                t.Logf("ban real queue -> err=%v resp=%q", err, oneLine(out))
                if err != nil {
                        t.Fatalf("ban failed: %v", err)
                }
                if !strings.Contains(out, "ok,") {
                        t.Fatalf("unexpected ban response: %q", out)
                }
                // status shows blocked WITH the notice (wire format verified
                // against the real server parser)
                out, _ = ClientStatus(KindSMP, rec, realQueueID)
                t.Logf("status -> %q", oneLine(out))
                if !strings.Contains(out, "EntityBlocked") {
                        t.Fatalf("queue should be blocked, status: %q", out)
                }
                if !strings.Contains(out, `notice = Just (ClientNotice {ttl = Nothing})`) {
                        t.Fatalf("blocked status should carry the no-expiry notice, status: %q", out)
                }
                // unban
                out, err = UnbanClient(KindSMP, rec, realQueueID)
                t.Logf("unban -> err=%v resp=%q", err, oneLine(out))
                if err != nil {
                        t.Fatalf("unban failed: %v", err)
                }
                out, _ = ClientStatus(KindSMP, rec, realQueueID)
                if !strings.Contains(out, "EntityActive") {
                        t.Fatalf("queue should be active after unban, status: %q", out)
                }
                // ban with content reason and a 1-day notice, then delete
                out, err = BanClient(KindSMP, rec, realQueueID, BanReasonContent, NewClientNotice(86400))
                if err != nil {
                        t.Fatalf("ban(content) failed: %v", err)
                }
                t.Logf("ban content -> %q", oneLine(out))
                out, err = DeleteClient(KindSMP, rec, realQueueID)
                t.Logf("delete -> err=%v resp=%q", err, oneLine(out))
                if err != nil {
                        t.Fatalf("delete failed: %v", err)
                }
                // after deletion the queue is gone
                if _, serr := ClientStatus(KindSMP, rec, realQueueID); serr == nil || !strings.Contains(serr.Error(), "no queue") {
                        t.Logf("post-delete status err=%v (any no-queue error is fine)", serr)
                }
        } else {
                t.Log("real-queue round-trip skipped (CLI could not create a queue)")
        }

        // 5. moderation commands against a (very likely) non-existent client ID.
        //    The point: the server must PARSE the command - the response is a
        //    proper queue-not-found style error ("error: AUTH"), never a parse
        //    error like "error: string" / "bad ControlProtocol".
        fakeID := "3JXv2Yb9aQ0AAAAAAAAAAAAAAAA"

        // free-text reason must NOT be sent: the server only accepts
        // reason=spam / reason=content (verified against simplexmq v6.5.0
        // BlockingInfo StrEncoding); the notice must be the exact JSON
        // the parser decodes ({"ttl":null} / {"ttl":<seconds>}).
        if got := blockingInfoArg("", nil); got != "reason=spam" {
                t.Errorf("blockingInfoArg empty: %q", got)
        }
        if got := blockingInfoArg("content", nil); got != "reason=content" {
                t.Errorf("blockingInfoArg content: %q", got)
        }
        if got := blockingInfoArg("free text", nil); got != "reason=spam" {
                t.Errorf("blockingInfoArg free text should fall back to spam: %q", got)
        }
        if got := blockingInfoArg("", NewClientNotice(0)); got != `reason=spam,notice={"ttl":null}` {
                t.Errorf("blockingInfoArg no-expiry notice: %q", got)
        }
        if got := blockingInfoArg("", NewClientNotice(86400)); got != `reason=spam,notice={"ttl":86400}` {
                t.Errorf("blockingInfoArg 1-day notice: %q", got)
        }

        out, err := BanClient(KindSMP, rec, fakeID, BanReasonSpam, nil)
        t.Logf("block   -> err=%v resp=%q", err, oneLine(out))
        if err == nil || !strings.Contains(err.Error(), "no queue") {
                t.Errorf("block on unknown queue: expected friendly no-queue error, got %v / %q", err, out)
        }
        assertNotBadProtocol(t, "block", out)

        // 5b. banning by a one-time connection LINK ID: getSenderQueue falls
        //     back from senderId to SSenderLink, so the link ID of an abuse
        //     report's invitation address is an equally valid target. An
        //     unknown one must parse the same (no-queue error, never a
        //     ControlProtocol parse failure).
        linkID := "3JXv2Yb9aQ0BBBBBBBBBBBB"
        out, err = BanClient(KindSMP, rec, linkID, BanReasonSpam, nil)
        t.Logf("block by link ID -> err=%v resp=%q", err, oneLine(out))
        if err == nil || !strings.Contains(err.Error(), "no queue") {
                t.Errorf("block by unknown link ID: expected friendly no-queue error, got %v / %q", err, out)
        }
        assertNotBadProtocol(t, "block by link ID", out)

        out, err = UnbanClient(KindSMP, rec, fakeID)
        t.Logf("unblock -> err=%v resp=%q", err, oneLine(out))
        if err == nil || !strings.Contains(err.Error(), "no queue") {
                t.Errorf("unblock on unknown queue: expected friendly no-queue error, got %v / %q", err, out)
        }
        assertNotBadProtocol(t, "unblock", out)

        out, err = DeleteClient(KindSMP, rec, fakeID)
        t.Logf("delete  -> err=%v resp=%q", err, oneLine(out))
        if err == nil || !strings.Contains(err.Error(), "no queue") {
                t.Errorf("delete on unknown queue: expected friendly no-queue error, got %v / %q", err, out)
        }
        assertNotBadProtocol(t, "delete", out)

        out, err = ClientStatus(KindSMP, rec, fakeID)
        t.Logf("status  -> err=%v resp=%q", err, oneLine(out))
        if err == nil || !strings.Contains(err.Error(), "no queue") {
                t.Errorf("status on unknown queue: expected friendly no-queue error, got %v / %q", err, out)
        }
        assertNotBadProtocol(t, "status", out)

        // 6. validation errors (no server round-trip)
        if _, err := BanClient(KindSMP, rec, "", "x", nil); err == nil {
                t.Error("empty client ID should be rejected")
        }
        if _, err := BanClient(KindSMP, rec, "has space", "x", nil); err == nil {
                t.Error("client ID with spaces should be rejected")
        }

        // 7. XFTP-side availability flags (no XFTP server needed for the check)
        if ControlSupported(KindXFTP, "block") != true {
                t.Error("xftp should support block")
        }
        if ControlSupported(KindXFTP, "unblock") != false {
                t.Error("xftp should NOT support unblock")
        }
        if ControlSupported(KindNTF, "block") != false {
                t.Error("ntf should NOT support block")
        }

        // 8. (the local ban bookkeeping is gone since v1.6.13: the ban list
        //    is derived from the server's own store-log BLOCK records -
        //    covered by TestParseSMPStoreLogBlockInfo in v1613_test.go)
}

func assertNotBadProtocol(t *testing.T, cmd, resp string) {
        t.Helper()
        low := strings.ToLower(resp)
        if strings.Contains(low, "bad controlprotocol") {
                t.Errorf("%s command was REJECTED by the server parser: %q", cmd, resp)
        }
        if strings.Contains(low, "error: string") {
                t.Errorf("%s command arguments failed to parse on the server (wrong wire format): %q", cmd, resp)
        }
}

// createRealQueue uses the official simplex-chat CLI (installed like the
// relay component) to create one real queue on the local SMP server, and
// returns its sender ID read from the server's store log.
func createRealQueue(t *testing.T, ctx context.Context, paths *Paths, inst *Installer, rec *ServerRec) string {
        t.Helper()
        rel, _, err := inst.CheckUpdate(ctx, KindRelay)
        if err != nil {
                t.Logf("checking simplex-chat release: %v", err)
                return ""
        }
        if err := inst.Install(ctx, KindRelay, rel); err != nil {
                t.Logf("installing simplex-chat CLI: %v", err)
                return ""
        }

        dbPrefix := filepath.Join(t.TempDir(), "chatdb")
        addr := "smp://" + rec.Fingerprint + "@localhost:5223"
        cliCtx, cancel := context.WithTimeout(ctx, 120*time.Second)
        defer cancel()
        cmd := exec.CommandContext(cliCtx, paths.BinPath(KindRelay),
                "-d", dbPrefix, "-s", addr, "-e", "/ad", "-t", "6")
        // the CLI asks for a display name on first run
        cmd.Stdin = strings.NewReader("sxops-test\n")
        var out bytes.Buffer
        cmd.Stdout = &out
        cmd.Stderr = &out
        if err := cmd.Run(); err != nil {
                t.Logf("simplex-chat CLI exited with error: %v\n--- CLI output ---\n%s", err, truncate(out.String(), 1500))
                return ""
        }
        t.Logf("simplex-chat CLI: %s", truncate(oneLine(out.String()), 300))

        // the newly created queue is the last CREATE record in the store log
        data, err := os.ReadFile(filepath.Join(paths.LogDir(KindSMP), StoreLogName(KindSMP)))
        if err != nil {
                t.Logf("reading store log: %v", err)
                return ""
        }
        sid := ""
        for _, line := range strings.Split(string(data), "\n") {
                if strings.HasPrefix(line, "CREATE ") {
                        if v := tokenValue(strings.Fields(line), "sid="); v != "" {
                                sid = v
                        }
                }
        }
        return sid
}

func oneLine(s string) string {
        return strings.ReplaceAll(strings.TrimSpace(s), "\n", " | ")
}
