package core

import (
        "bufio"
        "fmt"
        "net"
        "os"
        "path/filepath"
        "strings"
        "sync"
        "testing"
        "time"
)

// v1.6.8 fixes the moderation plumbing end to end:
//
//   - the SMP store-log parser ignored BLOCK/UNBLOCK/DELETE/SUSPEND
//     records (they carry the recipient ID positionally, not as "rid="),
//     so the admin queue list never reflected bans made through it;
//   - the XFTP store-log parser returned the FNEW sender file IDs, but
//     the XFTP control port resolves files through the RECIPIENT map -
//     every ban/delete with those IDs failed with "no queue"; the list
//     is now built from FADD recipient records (FACK retires one);
//   - the control-port client now drains the full two-line banner,
//     rejects wrong passwords at auth ("Current role is CPRNone"),
//     reports a dead command instead of returning an empty success and
//     detects the end of a response with a short rolling inactivity
//     window (every command used to cost a full 4s read deadline, which
//     kept the admin panel busy long enough to silently swallow clicks
//     on Ban/Unban/Delete);
//   - the moderation confirmation dialog now closes the moment the user
//     confirms - it used to stay open over the result area while the
//     command ran in the background, looking like a frozen dialog;
//   - restored store-log state parses the REAL ServerEntityStatus wire
//     tokens ("status=blocked,reason=.." in SMP CREATE lines, the bare
//     "blocked,.." / "off" trailing token in XFTP FNEW records) - bans
//     used to lose their "banned" listing after a server restart;
//   - the XFTP storage quota setting gained "Off (no limit)" as the new
//     default (the INI key must be ABSENT then - upstream reads a
//     missing key as unlimited, an empty one would abort startup).
//
// The wire-level round trips below run against a protocol-accurate fake
// (responses mirrored from upstream Server.hs), and the live
// TestBanProtocolLive (go test -tags=integration) proves the same flow
// against a real downloaded smp-server.
//
// The suspend/resume buttons were removed: the servers answer those
// commands with "not implemented".

// TestParseSMPStoreLogModerationRecords pins the positional record
// format: BLOCK/UNBLOCK/DELETE/SUSPEND lines carry the recipient ID as
// the second token, with no "rid=" prefix (tuple-encoded upstream).
func TestParseSMPStoreLogModerationRecords(t *testing.T) {
        log := strings.Join([]string{
                "CREATE rid=RIDA rk=k1 rdh=d1 sid=SID1 sk=s1",
                "CREATE rid=RIDB rk=k2 rdh=d2 sid=SID2 sk=s2",
                "CREATE rid=RIDC rk=k3 rdh=d3 sid=SID3 sk=s3",
                // ban SID1's queue: positional rid
                "BLOCK RIDA reason=spam",
                // ban SID2's queue then lift it
                "BLOCK RIDB reason=content",
                "UNBLOCK RIDB",
                // SID3's queue is deleted outright
                "DELETE RIDC",
                // a BLOCK for an unknown rid must not crash or invent rows
                "BLOCK UNKNOWNRID reason=spam",
        }, "\n")
        rows := ParseStoreLog(KindSMP, []byte(log))
        if len(rows) != 2 {
                t.Fatalf("got %d rows (%v), want 2", len(rows), rows)
        }
        byID := map[string]QueueInfo{}
        for _, r := range rows {
                byID[r.ID] = r
        }
        if q := byID["SID1"]; !q.Blocked {
                t.Errorf("SID1 should be blocked after the BLOCK record: %+v", q)
        }
        if q := byID["SID2"]; q.Blocked {
                t.Errorf("SID2 should be active after BLOCK+UNBLOCK: %+v", q)
        }
        if _, ok := byID["SID3"]; ok {
                t.Error("SID3 must be gone after DELETE")
        }
}

// TestParseSMPStoreLogBlockedOnCreate covers queues restored from a
// state snapshot: the compacted CREATE line carries the REAL
// ServerEntityStatus token ("status=blocked,reason=.." - QueueStore.hs
// StrEncoding), not the Haskell constructor name sxops used to match.
func TestParseSMPStoreLogBlockedOnCreate(t *testing.T) {
        // live block record + restored (post-restart, compacted) log
        log := strings.Join([]string{
                "CREATE rid=RIDA rk=k rdh=d sid=SID1 sk=s status=blocked,reason=spam",
                "CREATE rid=RIDB rk=k rdh=d sid=SID2 sk=s status=active",
                "CREATE rid=RIDC rk=k rdh=d sid=SID3 sk=s",
        }, "\n")
        rows := ParseStoreLog(KindSMP, []byte(log))
        if len(rows) != 3 {
                t.Fatalf("rows = %+v, want three rows", rows)
        }
        byID := map[string]QueueInfo{}
        for _, r := range rows {
                byID[r.ID] = r
        }
        if !byID["SID1"].Blocked {
                t.Errorf("SID1 should be blocked after restore: %+v", byID["SID1"])
        }
        if byID["SID2"].Blocked {
                t.Errorf("SID2 (status=active) must not be blocked: %+v", byID["SID2"])
        }
        if byID["SID3"].Blocked {
                t.Errorf("SID3 (no status token) must not be blocked: %+v", byID["SID3"])
        }
}

// TestParseXFTPStoreLogRecipients pins the v1.6.8 XFTP list: one row
// per live file RECIPIENT (the accepted moderation target), with the
// file-level blocked state, FACK removing a finished recipient and
// FDEL removing the whole file. FNEW status tokens are the bare
// ServerEntityStatus words ("active" / "blocked,.." / "off").
func TestParseXFTPStoreLogRecipients(t *testing.T) {
        log := strings.Join([]string{
                // file 1 with two recipients; one acknowledges (downloaded)
                "FNEW S1 keyAAAA 102400 digAAAA 1761000000 active",
                "FADD S1 R1:keyAAAA,R2:keyBBBB",
                "FACK R1",
                // file 2 with one recipient, later blocked
                "FNEW S2 keyBBBB 204800 digBBBB 1761000500 active",
                "FADD S2 R3:keyCCCC",
                "FBLK S2 reason=spam",
                // file 3 deleted
                "FNEW S3 keyCCCC 409600 digCCCC 1761000600 active",
                "FADD S3 R4:keyDDDD",
                "FDEL S3",
                // file 4 restored already blocked (compacted log)
                "FNEW S4 keyDDDD 819200 digDDDD 1761000700 blocked,reason=content",
                "FADD S4 R5:keyEEEE",
                // file 5 retired ("off") - not a moderation target
                "FNEW S5 keyEEEE 1638400 digEEEE 1761000800 off",
                "FADD S5 R6:keyFFFF",
        }, "\n")
        rows := ParseStoreLog(KindXFTP, []byte(log))
        if len(rows) != 3 {
                t.Fatalf("got %d rows (%v), want 3 (R2, R3, R5)", len(rows), rows)
        }
        if rows[0].ID != "R2" {
                t.Errorf("first row = %q, want R2 (R1 acknowledged away)", rows[0].ID)
        }
        if rows[0].Blocked {
                t.Errorf("R2 must not be blocked: %+v", rows[0])
        }
        if rows[1].ID != "R3" || !rows[1].Blocked {
                t.Errorf("second row = %+v, want R3 blocked", rows[1])
        }
        if rows[2].ID != "R5" || !rows[2].Blocked {
                t.Errorf("third row = %+v, want R5 blocked (restored FNEW status)", rows[2])
        }
}

// TestParseXFTPStoreLogFaddOnlyTail: an FADD whose FNEW fell outside
// the read tail must still yield usable rows (shadow file entry).
func TestParseXFTPStoreLogFaddOnlyTail(t *testing.T) {
        rows := ParseStoreLog(KindXFTP, []byte("FADD S9 R9:keyZZZZ\n"))
        if len(rows) != 1 || rows[0].ID != "R9" {
                t.Fatalf("rows = %+v, want one row for R9", rows)
        }
}

// fakeControlServer is a minimal simplexmq control port that mirrors
// the REAL upstream handlers (v6.5.0/stable Server.hs): two-line
// banner, auth role line, and the moderation commands with their
// actual response strings - including "error: AUTH" for unknown IDs
// and the daily block quota.
type fakeControlServer struct {
        ln     net.Listener
        authOK bool
        proto  string // "smp" | "xftp" command set

        mu     sync.Mutex
        got    []string
        queues map[string]string // senderId -> "active" | "blocked,reason=.."
        links  map[string]string // one-time linkId -> senderId (SSenderLink fallback)
        files  map[string]string // xftp recipient fileId -> "active" | "blocked"
        quota  int               // 0 = unlimited; else max blocks before the daily limit error
        blocks int
}

func newFakeControlServer(t *testing.T, authOK bool) *fakeControlServer {
        t.Helper()
        ln, err := net.Listen("tcp", "127.0.0.1:0")
        if err != nil {
                t.Fatal(err)
        }
        f := &fakeControlServer{
                ln:     ln,
                authOK: authOK,
                proto:  "smp",
                queues: map[string]string{},
                links:  map[string]string{},
                files:  map[string]string{},
        }
        go f.serve()
        t.Cleanup(func() { _ = ln.Close() })
        return f
}

// asXFTP switches the fake to the smaller XFTP command set.
func (f *fakeControlServer) asXFTP() *fakeControlServer {
        f.mu.Lock()
        f.proto = "xftp"
        f.mu.Unlock()
        return f
}

// addQueue seeds a sender queue ("active" by default).
func (f *fakeControlServer) addQueue(sid string) *fakeControlServer {
        f.mu.Lock()
        f.queues[sid] = "active"
        f.mu.Unlock()
        return f
}

// addLink registers a one-time connection link whose link ID resolves to
// the given queue - the servers' getSenderQueue SSenderLink fallback, so
// block / unblock / delete / status accept the link ID as the target.
func (f *fakeControlServer) addLink(linkID, sid string) *fakeControlServer {
        f.mu.Lock()
        f.links[linkID] = sid
        f.mu.Unlock()
        return f
}

// addFile seeds an XFTP recipient file ID.
func (f *fakeControlServer) addFile(rid string) *fakeControlServer {
        f.mu.Lock()
        f.files[rid] = "active"
        f.mu.Unlock()
        return f
}

// setQuota arms the daily block quota (0 = unlimited).
func (f *fakeControlServer) setQuota(n int) *fakeControlServer {
        f.mu.Lock()
        f.quota = n
        f.mu.Unlock()
        return f
}

func (f *fakeControlServer) port() int { return f.ln.Addr().(*net.TCPAddr).Port }

func (f *fakeControlServer) log(line string) {
        f.mu.Lock()
        f.got = append(f.got, line)
        f.mu.Unlock()
}

func (f *fakeControlServer) wireLog() string {
        f.mu.Lock()
        defer f.mu.Unlock()
        return strings.Join(f.got, "|")
}

// blockedNow reports whether the queue is currently blocked.
func (f *fakeControlServer) blockedNow(sid string) bool {
        f.mu.Lock()
        defer f.mu.Unlock()
        return strings.HasPrefix(f.queues[sid], "blocked")
}

func (f *fakeControlServer) serve() {
        for {
                conn, err := f.ln.Accept()
                if err != nil {
                        return
                }
                go func(conn net.Conn) {
                        defer conn.Close()
                        conn.Write([]byte("SMP server control port\n'help' for supported commands\n"))
                        sc := bufio.NewScanner(conn)
                        for sc.Scan() {
                                line := strings.TrimSpace(sc.Text())
                                f.log(line)
                                f.mu.Lock()
                                proto := f.proto
                                f.mu.Unlock()
                                if resp, handled := f.command(proto, line); handled {
                                        if resp != "" {
                                                conn.Write([]byte(resp + "\n"))
                                        }
                                        if resp == "__close__" {
                                                return
                                        }
                                        continue
                                }
                                switch {
                                case strings.HasPrefix(line, "auth "):
                                        if f.authOK {
                                                conn.Write([]byte("Current role is CPRAdmin\n"))
                                        } else {
                                                conn.Write([]byte("Current role is CPRNone\n"))
                                        }
                                case line == "stats":
                                        conn.Write([]byte("fromTime: 1761000000\nqCount: 7\nClients: 2\n"))
                                case line == "clients":
                                        conn.Write([]byte("clientId,sessionId,connected,createdAt,rcvActiveAt,sndActiveAt,age,subscriptions\n"))
                                        conn.Write([]byte("3,abc,True,1761000000,1761000012,1761000030,600,1\n"))
                                case line == "quit":
                                        return
                                default:
                                        conn.Write([]byte("error: bad ControlProtocol command\n"))
                                }
                        }
                }(conn)
        }
}

// command implements the moderation wire semantics of the real
// servers; the response strings mirror upstream Server.hs verbatim so
// sxops error mapping is exercised against the real shapes.
func (f *fakeControlServer) command(proto, line string) (string, bool) {
        fs := strings.Fields(line)
        if len(fs) == 0 {
                return "", false
        }
        f.mu.Lock()
        defer f.mu.Unlock()
        switch proto {
        case "smp":
                // resolve mirrors the servers' getSenderQueue: senderId
                // first, then the one-time link ID fallback; a link whose
                // queue is gone resolves to nothing (AUTH).
                resolve := func(id string) (string, bool) {
                        if _, ok := f.queues[id]; ok {
                                return id, true
                        }
                        if sid, ok := f.links[id]; ok {
                                if _, live := f.queues[sid]; live {
                                        return sid, true
                                }
                        }
                        return id, false
                }
                switch fs[0] {
                case "block":
                        if len(fs) != 3 {
                                return "error: bad ControlProtocol command", true
                        }
                        sid, info := fs[1], fs[2]
                        sid, ok := resolve(sid)
                        if !ok {
                                return "error: AUTH", true // getSenderQueue miss
                        }
                        st := f.queues[sid]
                        if f.quota != 0 && f.blocks >= f.quota {
                                return fmt.Sprintf("error: reached limit of %d queues blocked daily", f.quota), true
                        }
                        if strings.HasPrefix(st, "blocked") {
                                // same info -> skip; different info upstream
                                // re-blocks, both answer "already inactive"
                                return "ok, already inactive: " + blockedShow(info), true
                        }
                        f.queues[sid] = "blocked," + info
                        f.blocks++
                        return "ok, queue blocked: " + blockedShow(info), true
                case "unblock":
                        if len(fs) != 2 {
                                return "error: bad ControlProtocol command", true
                        }
                        sid, ok := resolve(fs[1])
                        if !ok {
                                return "error: AUTH", true
                        }
                        st := f.queues[sid]
                        switch {
                        case strings.HasPrefix(st, "blocked,"):
                                info := strings.TrimPrefix(st, "blocked,")
                                f.queues[sid] = "active"
                                // live v6.5.0: "ok, queue unblocked, reason to block was: BlockingInfo {...}"
                                return "ok, queue unblocked, reason to block was: BlockingInfo {reason = " + brShow(info) + ", notice = " + noticeShow(info) + "}", true
                        case st == "active":
                                return "ok, queue was active", true
                        default:
                                return "error, queue is inactive", true
                        }
                case "delete":
                        if len(fs) != 2 {
                                return "error: bad ControlProtocol command", true
                        }
                        sid, ok := resolve(fs[1])
                        if !ok {
                                return "error: AUTH", true
                        }
                        delete(f.queues, sid)
                        for l, s := range f.links {
                                if s == sid {
                                        delete(f.links, l)
                                }
                        }
                        return "ok, 3 messages deleted", true
                case "status":
                        if len(fs) != 2 {
                                return "error: bad ControlProtocol command", true
                        }
                        sid, ok := resolve(fs[1])
                        if !ok {
                                return "error: AUTH", true
                        }
                        st := f.queues[sid]
                        if strings.HasPrefix(st, "blocked,") {
                                return "status: " + blockedShow(strings.TrimPrefix(st, "blocked,")) + ", updatedAt: 2026-01-01 00:00:00 UTC, queueMode: QMMessaging", true
                        }
                        return "status: EntityActive, updatedAt: 2026-01-01 00:00:00 UTC, queueMode: QMMessaging", true
                }
        case "xftp":
                switch fs[0] {
                case "block":
                        if len(fs) != 3 {
                                return "error: bad ControlProtocol command", true
                        }
                        st, ok := f.files[fs[1]]
                        if !ok {
                                return "error: AUTH", true // getFile SFRecipient miss
                        }
                        _ = st
                        f.files[fs[1]] = "blocked"
                        return "ok", true // blockServerFile: ok
                case "delete":
                        if len(fs) != 2 {
                                return "error: bad ControlProtocol command", true
                        }
                        if _, ok := f.files[fs[1]]; !ok {
                                return "error: AUTH", true
                        }
                        delete(f.files, fs[1])
                        return "ok", true
                }
        }
        return "", false
}

// blockedShow renders the derived-Show text the real server prints for
// a blocked entity: "EntityBlocked (BlockingInfo {reason = BRSpam, notice = ...})".
func blockedShow(info string) string {
        return "EntityBlocked (BlockingInfo {reason = " + brShow(info) + ", notice = " + noticeShow(info) + "})"
}

// noticeShow renders the notice part of the derived-Show output from the
// wire info token: reason=spam,notice={"ttl":null} renders as
// "Just (ClientNotice {ttl = Nothing})", {"ttl":86400} as
// "Just (ClientNotice {ttl = Just 86400})", no notice as "Nothing".
func noticeShow(info string) string {
        const marker = `,notice={"ttl":`
        i := strings.Index(info, marker)
        if i < 0 {
                return "Nothing"
        }
        rest := strings.TrimSuffix(info[i+len(marker):], "}")
        if rest == "null" {
                return "Just (ClientNotice {ttl = Nothing})"
        }
        return "Just (ClientNotice {ttl = Just " + rest + "})"
}

// brShow maps a wire "reason=.." token to its Haskell constructor name.
func brShow(info string) string {
        if strings.Contains(info, "content") {
                return "BRContent"
        }
        return "BRSpam"
}

// TestControlClientFastAndCorrect pins the v1.6.8 client behaviour:
// the whole banner is drained (the second line never leaks into the
// first response), stats parse correctly, and Dial+Run complete far
// faster than the old fixed 4s-per-command read deadline allowed.
func TestControlClientFastAndCorrect(t *testing.T) {
        f := newFakeControlServer(t, true)
        rec := &ServerRec{ControlPort: f.port(), ControlAdmin: "tok"}

        start := time.Now()
        st, err := FetchStats(KindSMP, rec)
        if err != nil {
                t.Fatal(err)
        }
        elapsed := time.Since(start)
        // the old client blocked for a full 4s read deadline per command
        // (~8s for banner+auth+stats); the rolling window keeps the whole
        // round trip around one second
        if elapsed > 3*time.Second {
                t.Errorf("FetchStats took %v - the rolling response window is not effective", elapsed)
        }
        if st.Get("qCount") != "7" {
                t.Errorf("qCount = %q, want 7 (banner line leaked into the response?)", st.Get("qCount"))
        }
        if st.Get("clients") != "2" {
                t.Errorf("clients (case-insensitive) = %q, want 2", st.Get("clients"))
        }
        if st.Get("fromTime") != "1761000000" {
                t.Errorf("fromTime = %q", st.Get("fromTime"))
        }

        // clients CSV parses with the epoch fields intact
        raw, err := FetchRaw(KindSMP, rec, "clients", "")
        if err != nil {
                t.Fatal(err)
        }
        rows := ParseClients(raw)
        if len(rows) != 1 {
                t.Fatalf("clients rows = %d, want 1 (raw=%q)", len(rows), raw)
        }
        if rows[0].CreatedAt != "1761000000" || rows[0].Age != "600" {
                t.Errorf("row = %+v", rows[0])
        }
}

// TestControlClientAuthRejected: a wrong password answers
// "Current role is CPRNone" - Dial must fail instead of letting the
// next command die with a bare "AUTH" line.
func TestControlClientAuthRejected(t *testing.T) {
        f := newFakeControlServer(t, false)
        rec := &ServerRec{ControlPort: f.port(), ControlAdmin: "wrong"}
        if _, err := FetchStats(KindSMP, rec); err == nil {
                t.Fatal("auth with a rejected password must fail")
        } else if !strings.Contains(err.Error(), "rejected") {
                t.Errorf("err = %v, want a clear rejection message", err)
        }
}

// TestControlClientNoResponseIsAnError: a server that never answers
// must surface as an error, not as an empty success.
func TestControlClientNoResponseIsAnError(t *testing.T) {
        ln, err := net.Listen("tcp", "127.0.0.1:0")
        if err != nil {
                t.Fatal(err)
        }
        defer ln.Close()
        go func() {
                for {
                        conn, err := ln.Accept()
                        if err != nil {
                                return
                        }
                        // banner only - never answers commands
                        conn.Write([]byte("SMP server control port\n'help' for supported commands\n"))
                }
        }()
        rec := &ServerRec{ControlPort: ln.Addr().(*net.TCPAddr).Port, ControlAdmin: "tok"}
        if _, err := FetchStats(KindSMP, rec); err == nil {
                t.Fatal("a command with no response must return an error, not an empty success")
        }
}

// TestControlCommandsNoSuspendResume: suspend/resume are "not
// implemented" upstream - they must not be offered as supported.
func TestControlCommandsNoSuspendResume(t *testing.T) {
        for _, cmd := range []string{"suspend", "resume"} {
                for _, k := range []Kind{KindSMP, KindXFTP, KindNTF} {
                        if ControlSupported(k, cmd) {
                                t.Errorf("%s must not advertise %q (upstream answers \"not implemented\")", k, cmd)
                        }
                }
        }
}

// TestApplyPortableProfileQuotaOff pins the off/default quota encoding:
// the storage_quota key is commented out (absent for the server), never
// set to an empty value; an explicit quota is written as before.
func TestApplyPortableProfileQuotaOff(t *testing.T) {
        for _, tc := range []struct {
                name    string
                quota   int
                wantSet bool
        }{
                {"off default", 0, false},
                {"off negative", -5, false},
                {"explicit", 25, true},
        } {
                t.Run(tc.name, func(t *testing.T) {
                        path := filepath.Join(t.TempDir(), "file-server.ini")
                        xftpTemplateIni(t, path)
                        ini, err := LoadIni(path)
                        if err != nil {
                                t.Fatal(err)
                        }
                        applyPortableProfile(ini, KindXFTP, Settings{XFTPQuotaGB: tc.quota}, &ServerRec{Kind: KindXFTP}, 0, "")
                        got, ok := ini.Get("FILES", "storage_quota")
                        if tc.wantSet {
                                if !ok || got != "25gb" {
                                        t.Errorf("storage_quota = %q (ok=%v), want 25gb", got, ok)
                                }
                                if !strings.Contains(ini.Text(), "storage_quota = 25gb") {
                                        t.Errorf("INI text missing the quota line:\n%s", ini.Text())
                                }
                        } else {
                                if ok {
                                        t.Errorf("storage_quota must be absent when off, got %q\n%s", got, ini.Text())
                                }
                                if strings.Contains(ini.Text(), "storage_quota =\n") {
                                        t.Errorf("storage_quota must never be an EMPTY value (startup abort):\n%s", ini.Text())
                                }
                                if !strings.Contains(ini.Text(), "# storage_quota") {
                                        t.Errorf("expected the template key commented out:\n%s", ini.Text())
                                }
                        }
                        // expiry is written either way
                        if got, _ := ini.Get("STORE_LOG", "expire_files_hours"); got != "48" {
                                t.Errorf("expire_files_hours = %q, want 48", got)
                        }
                })
        }
}

// TestStateQuotaOffMigration pins the one-time v1 -> v2 state
// migration and that later deliberate choices survive reloads.
func TestStateQuotaOffMigration(t *testing.T) {
        writeState := func(json string) *StateStore {
                paths := testPaths(t)
                if err := os.WriteFile(paths.StateFile(), []byte(json), 0o600); err != nil {
                        t.Fatal(err)
                }
                st, err := NewStateStore(paths)
                if err != nil {
                        t.Fatal(err)
                }
                return st
        }

        // v1 state with the old silent default 10 -> off, version bumped
        st := writeState(`{"version": 1, "settings": {"xftp_quota_gb": 10}, "servers": {}}`)
        if q := st.Settings().XFTPQuotaGB; q != 0 {
                t.Errorf("v1 quota 10 should migrate to 0 (off), got %d", q)
        }
        if v := st.Get().Version; v != 2 {
                t.Errorf("version = %d, want 2", v)
        }

        // v1 state with a non-default choice is kept
        st = writeState(`{"version": 1, "settings": {"xftp_quota_gb": 50}, "servers": {}}`)
        if q := st.Settings().XFTPQuotaGB; q != 50 {
                t.Errorf("v1 quota 50 must be kept, got %d", q)
        }

        // v2 state with a deliberate 10 keeps it across reloads
        st = writeState(`{"version": 2, "settings": {"xftp_quota_gb": 10}, "servers": {}}`)
        if q := st.Settings().XFTPQuotaGB; q != 10 {
                t.Errorf("v2 quota 10 must be kept (explicit choice), got %d", q)
        }

        // a fresh state defaults to off
        paths := testPaths(t)
        st, err := NewStateStore(paths)
        if err != nil {
                t.Fatal(err)
        }
        if q := st.Settings().XFTPQuotaGB; q != 0 {
                t.Errorf("fresh state quota = %d, want 0 (off default)", q)
        }
        if v := st.Get().Version; v != 2 {
                t.Errorf("fresh state version = %d, want 2", v)
        }
}

// TestStatsCaseInsensitive covers the capitalization difference
// (upstream prints "Clients", sxops asks for "clients").
func TestStatsCaseInsensitive(t *testing.T) {
        st := Stats{"Clients": "4", "qCount": "9"}
        if st.Get("clients") != "4" {
                t.Errorf("Get(clients) = %q, want 4", st.Get("clients"))
        }
        if st.Get("qcount") != "9" {
                t.Errorf("Get(qcount) = %q, want 9", st.Get("qcount"))
        }
        if st.Get("missing") != "0" {
                t.Errorf("Get(missing) = %q, want 0", st.Get("missing"))
        }
}

// TestRecordID covers the positional/tolerant ID extraction.
func TestRecordID(t *testing.T) {
        if got := recordID([]string{"BLOCK", "RID1", "reason=spam"}); got != "RID1" {
                t.Errorf("recordID positional = %q", got)
        }
        if got := recordID([]string{"BLOCK", "rid=RID2"}); got != "RID2" {
                t.Errorf("recordID token fallback = %q", got)
        }
        if got := recordID([]string{"BLOCK"}); got != "" {
                t.Errorf("recordID empty = %q", got)
        }
}

// TestBlockingInfoArg stays pinned from the integration suite.
func TestBlockingInfoArg(t *testing.T) {
        if got := blockingInfoArg("", nil); got != "reason=spam" {
                t.Errorf("empty -> %q", got)
        }
        if got := blockingInfoArg("content", nil); got != "reason=content" {
                t.Errorf("content -> %q", got)
        }
        if got := blockingInfoArg("whatever", nil); got != "reason=spam" {
                t.Errorf("free text -> %q", got)
        }
}

// TestControlResponseErrorFriendlyAuth: "error: AUTH" is the servers'
// unknown-ID answer and must map to the friendly message - "no queue"
// on the chat server, "no file" on the file server (v1.6.18).
func TestControlResponseErrorFriendlyAuth(t *testing.T) {
        err := controlResponseError(KindSMP, "error: AUTH\n")
        if err == nil || !strings.Contains(err.Error(), "no queue") {
                t.Errorf("error: AUTH should be friendly, got %v", err)
        }
        err = controlResponseError(KindSMP, "AUTH\n")
        if err == nil || !strings.Contains(err.Error(), "wrong control password") {
                t.Errorf("bare AUTH should flag the password, got %v", err)
        }
        if err := controlResponseError(KindSMP, "ok, queue blocked: EntityBlocked {blockReason = ...}\n"); err != nil {
                t.Errorf("success response must not error, got %v", err)
        }
        // the XFTP wording names FILES - the upstream getFile resolves
        // files through the recipient map and answers AUTH for unknown IDs
        err = controlResponseError(KindXFTP, "error: AUTH\n")
        if err == nil || !strings.Contains(err.Error(), "no file") {
                t.Errorf("xftp error: AUTH should say no file, got %v", err)
        }
        if err := controlResponseError(KindXFTP, "ok\n"); err != nil {
                t.Errorf("xftp success must not error, got %v", err)
        }
}

// TestFetchRawRejectsUnsupported covers the uniform support check
// (commands the selected server does not understand never reach the
// wire, with or without arguments).
func TestFetchRawRejectsUnsupported(t *testing.T) {
        f := newFakeControlServer(t, true)
        rec := &ServerRec{ControlPort: f.port(), ControlAdmin: "tok"}
        if _, err := FetchRaw(KindXFTP, rec, "clients", ""); err == nil {
                t.Error("xftp does not support clients - must be rejected client-side")
        }
        if _, err := FetchRaw(KindSMP, rec, "nosuchcmd", ""); err == nil {
                t.Error("unknown command must be rejected client-side")
        }
        if strings.Contains(f.wireLog(), "nosuchcmd") {
                t.Errorf("rejected commands must not reach the wire, wire log: %s", f.wireLog())
        }
}

// ---- v1.6.8 final: full moderation round trips on the wire ----
//
// These exercise the exact bytes sxops sends and the exact response
// strings the real servers return (mirrored from upstream Server.hs /
// Control.hs), so a regression in command construction, banner
// handling, auth or error mapping fails here instead of in production.

// TestBanRoundTripSMP walks the whole moderation lifecycle against the
// protocol-accurate fake: ban -> status blocked -> unban -> status
// active -> ban with content reason -> idempotent re-ban -> delete.
func TestBanRoundTripSMP(t *testing.T) {
        f := newFakeControlServer(t, true).addQueue("SID1234567890abcdef")
        rec := &ServerRec{ControlPort: f.port(), ControlAdmin: "tok"}
        const sid = "SID1234567890abcdef"

        // 1. ban with the spam reason: wire bytes must be exactly
        //    "block <sid> reason=spam" (Control.hs CPBlock grammar).
        out, err := BanClient(KindSMP, rec, sid, BanReasonSpam, nil)
        if err != nil {
                t.Fatalf("ban failed: %v (out=%q)", err, out)
        }
        if !strings.Contains(out, "ok, queue blocked") {
                t.Fatalf("unexpected ban response: %q", out)
        }
        if !strings.Contains(f.wireLog(), "block "+sid+" reason=spam") {
                t.Fatalf("wire command wrong: %q", f.wireLog())
        }
        if !f.blockedNow(sid) {
                t.Fatal("fake says the queue is not blocked")
        }

        // 2. status reports the block
        out, err = ClientStatus(KindSMP, rec, sid)
        if err != nil {
                t.Fatalf("status failed: %v", err)
        }
        if !strings.Contains(out, "EntityBlocked") {
                t.Fatalf("status should be blocked: %q", out)
        }

        // 3. unban lifts it
        out, err = UnbanClient(KindSMP, rec, sid)
        if err != nil {
                t.Fatalf("unban failed: %v (out=%q)", err, out)
        }
        if !strings.Contains(out, "ok, queue unblocked") || !strings.Contains(f.wireLog(), "unblock "+sid) {
                t.Fatalf("unban round trip wrong: out=%q wire=%q", out, f.wireLog())
        }
        if f.blockedNow(sid) {
                t.Fatal("fake says the queue is still blocked after unban")
        }
        out, _ = ClientStatus(KindSMP, rec, sid)
        if !strings.Contains(out, "EntityActive") {
                t.Fatalf("status should be active after unban: %q", out)
        }

        // 4. ban with the content reason maps to reason=content on the wire
        out, err = BanClient(KindSMP, rec, sid, BanReasonContent, nil)
        if err != nil {
                t.Fatalf("ban(content) failed: %v", err)
        }
        if !strings.Contains(out, "BRContent") {
                t.Fatalf("content ban response: %q", out)
        }
        if !strings.Contains(f.wireLog(), "block "+sid+" reason=content") {
                t.Fatalf("content-reason wire command wrong: %q", f.wireLog())
        }

        // 5. banning an already-banned queue is a server "ok", not an error
        out, err = BanClient(KindSMP, rec, sid, BanReasonSpam, nil)
        if err != nil {
                t.Fatalf("re-ban should not error: %v (out=%q)", err, out)
        }
        if !strings.Contains(out, "already inactive") {
                t.Fatalf("re-ban response: %q", out)
        }

        // 6. delete removes the queue for good
        out, err = DeleteClient(KindSMP, rec, sid)
        if err != nil {
                t.Fatalf("delete failed: %v", err)
        }
        if !strings.Contains(out, "ok,") || !strings.Contains(f.wireLog(), "delete "+sid) {
                t.Fatalf("delete round trip wrong: out=%q wire=%q", out, f.wireLog())
        }
        // and the next status is a friendly no-queue error
        _, err = ClientStatus(KindSMP, rec, sid)
        if err == nil || !strings.Contains(err.Error(), "no queue") {
                t.Fatalf("status after delete should be a friendly no-queue error, got %v", err)
        }
}

// TestBanUnknownQueueFriendly: an ID the server does not know answers
// "error: AUTH" - sxops must surface the friendly no-queue message
// (the user pasted a wrong ID), never a raw protocol dump.
func TestBanUnknownQueueFriendly(t *testing.T) {
        f := newFakeControlServer(t, true)
        rec := &ServerRec{ControlPort: f.port(), ControlAdmin: "tok"}
        for _, tc := range []struct {
                name string
                fn   func() (string, error)
        }{
                {"ban", func() (string, error) { return BanClient(KindSMP, rec, "UNKNOWNID", BanReasonSpam, nil) }},
                {"unban", func() (string, error) { return UnbanClient(KindSMP, rec, "UNKNOWNID") }},
                {"delete", func() (string, error) { return DeleteClient(KindSMP, rec, "UNKNOWNID") }},
                {"status", func() (string, error) { return ClientStatus(KindSMP, rec, "UNKNOWNID") }},
        } {
                out, err := tc.fn()
                if err == nil || !strings.Contains(err.Error(), "no queue") {
                        t.Errorf("%s on unknown ID: want friendly no-queue error, got err=%v out=%q", tc.name, err, out)
                }
        }
}

// TestBanQuotaSurfaced: the daily block quota error must reach the
// user verbatim ("reached limit of N queues blocked daily").
func TestBanQuotaSurfaced(t *testing.T) {
        f := newFakeControlServer(t, true).addQueue("SIDQ").setQuota(1)
        rec := &ServerRec{ControlPort: f.port(), ControlAdmin: "tok"}
        if _, err := BanClient(KindSMP, rec, "SIDQ", BanReasonSpam, nil); err != nil {
                t.Fatalf("first ban should pass: %v", err)
        }
        f.addQueue("SIDQ2")
        out, err := BanClient(KindSMP, rec, "SIDQ2", BanReasonSpam, nil)
        if err == nil || !strings.Contains(err.Error(), "reached limit of 1 queues blocked daily") {
                t.Fatalf("quota error not surfaced: err=%v out=%q", err, out)
        }
}

// TestBanRoundTripXFTP: the file server blocks by RECIPIENT file ID
// (getFile SFRecipient), answers a bare "ok", and has NO unblock -
// sxops must reject unban client-side without touching the wire.
func TestBanRoundTripXFTP(t *testing.T) {
        f := newFakeControlServer(t, true).asXFTP().addFile("RCPRID1")
        rec := &ServerRec{ControlPort: f.port(), ControlAdmin: "tok"}

        out, err := BanClient(KindXFTP, rec, "RCPRID1", BanReasonSpam, nil)
        if err != nil {
                t.Fatalf("xftp ban failed: %v (out=%q)", err, out)
        }
        if strings.TrimSpace(out) != "ok" {
                t.Fatalf("xftp ban response: %q", out)
        }
        if !strings.Contains(f.wireLog(), "block RCPRID1 reason=spam") {
                t.Fatalf("xftp wire command wrong: %q", f.wireLog())
        }

        // unban does not exist on xftp - client-side rejection, no wire bytes
        if _, err := UnbanClient(KindXFTP, rec, "RCPRID1"); err == nil {
                t.Fatal("unban on xftp must be rejected client-side")
        }
        // presence-based: the previous connection's trailing "quit" is
        // logged asynchronously, so exact-equality would be flaky
        if strings.Contains(f.wireLog(), "unblock") {
                t.Fatalf("rejected unban must not touch the wire: %q", f.wireLog())
        }

        // delete by the same recipient ID
        if out, err = DeleteClient(KindXFTP, rec, "RCPRID1"); err != nil {
                t.Fatalf("xftp delete failed: %v (out=%q)", err, out)
        }
        if !strings.Contains(f.wireLog(), "delete RCPRID1") {
                t.Fatalf("xftp delete wire command wrong: %q", f.wireLog())
        }

        // unknown file ID -> friendly no-file error (v1.6.18 wording:
        // the file server resolves FILES, not queues)
        if out, err := BanClient(KindXFTP, rec, "NOPE", BanReasonSpam, nil); err == nil || !strings.Contains(err.Error(), "no file") {
                t.Errorf("xftp unknown ID: want friendly no-file error, got err=%v out=%q", err, out)
        }
}

// TestBanValidationNeverWires: malformed input is rejected before any
// connection is opened (no dial, no auth, no command).
func TestBanValidationNeverWires(t *testing.T) {
        f := newFakeControlServer(t, true)
        rec := &ServerRec{ControlPort: f.port(), ControlAdmin: "tok"}
        cases := []struct {
                name string
                fn   func() (string, error)
        }{
                {"empty", func() (string, error) { return BanClient(KindSMP, rec, "", BanReasonSpam, nil) }},
                {"spaces", func() (string, error) { return BanClient(KindSMP, rec, "has space", BanReasonSpam, nil) }},
                {"too long", func() (string, error) {
                        return BanClient(KindSMP, rec, strings.Repeat("x", 300), BanReasonSpam, nil)
                }},
        }
        for _, tc := range cases {
                if _, err := tc.fn(); err == nil {
                        t.Errorf("%s client ID must be rejected", tc.name)
                }
        }
        // only the test connection's own traffic may exist - no command was sent
        if strings.Contains(f.wireLog(), "block") {
                t.Fatalf("invalid IDs must not produce wire commands: %q", f.wireLog())
        }
}
