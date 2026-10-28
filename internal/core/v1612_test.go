package core

import (
        "os"
        "path/filepath"
        "strings"
        "testing"
        "time"
)

// v1.6.12: more official moderation surface - one-time connection
// link IDs as ban targets (store-log LINK/LDELETE records + the
// getSenderQueue SSenderLink fallback), full-address parsing in the ban
// form - plus the removal of the per-row hide buttons and the mark-mine
// field. Carried over from v1.6.11: the ban notice (ClientNotice ttl)
// wire format and the per-queue timestamps in the admin list.

// smpTemplateIni writes a realistic smp-server.ini (as written by init).
func smpTemplateIni(t *testing.T, path string) {
        t.Helper()
        content := `[INFORMATION]
source_code = https://github.com/simplex-chat/simplexmq

[STORE_LOG]
enable = on
store_msgs = off
log_stats = on

[AUTH]
# new_queues = on
control_port_admin_password = admin-token
control_port_user_password = user-token

[TRANSPORT]
host = <hostnames>
port = 5223
# control_port = 5224

[WEB]
static_path = /www
# http = 8000
`
        if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
                t.Fatal(err)
        }
}

// queuesTestEngine builds an engine + native backend over temp paths.
func queuesTestEngine(t *testing.T, k Kind, iniContent func(t *testing.T, path string)) (*Engine, *Paths, *StateStore) {
        t.Helper()
        paths := testPaths(t)
        state, err := NewStateStore(paths)
        if err != nil {
                t.Fatal(err)
        }
        if err := paths.Ensure(); err != nil {
                t.Fatal(err)
        }
        _ = state.UpdateServer(k, func(r *ServerRec) {
                r.Installed = true
                r.Enabled = true
        })
        sv := NewSupervisor(paths, state)
        eng := &Engine{Paths: paths, State: state, Sv: sv, Log: NopInstallLog(),
                Tor: NewTorManager(paths, state, NopInstallLog())}
        eng.Backend = &nativeBackend{eng: eng}
        if iniContent != nil {
                iniContent(t, filepath.Join(paths.CfgDir(k), Def(k).IniName))
        }
        return eng, paths, state
}

// TestApplyPortableProfilePreservesAuthGate: re-applying the portable
// profile (every settings save) must NEVER touch [AUTH] gates such as
// new_queues / new_files an operator set by hand in the INI - silently
// re-enabling link creation would undo a manually applied ban.
func TestApplyPortableProfilePreservesAuthGate(t *testing.T) {
        dir := t.TempDir()
        smpPath := filepath.Join(dir, "smp.ini")
        smpTemplateIni(t, smpPath)
        ini, err := LoadIni(smpPath)
        if err != nil {
                t.Fatal(err)
        }
        ini.Set("AUTH", "new_queues", "off")
        s := Settings{EnableInfoPages: true}
        rec := &ServerRec{OnionHost: "abc.onion"}
        applyPortableProfile(ini, KindSMP, s, rec, 0, "")
        if !strings.Contains(ini.Text(), "new_queues = off") {
                t.Fatalf("portable profile dropped the auth gate:\n%s", ini.Text())
        }

        xftpPath := filepath.Join(dir, "xftp.ini")
        xftpTemplateIni(t, xftpPath)
        xftp, err := LoadIni(xftpPath)
        if err != nil {
                t.Fatal(err)
        }
        xftp.Set("AUTH", "new_files", "off")
        applyPortableProfile(xftp, KindXFTP, s, rec, 0, "")
        if !strings.Contains(xftp.Text(), "new_files = off") {
                t.Fatalf("portable profile dropped the XFTP auth gate:\n%s", xftp.Text())
        }
}

// TestParseXFTPStoreLogCreatedAt: FNEW records carry the file creation
// time as epoch seconds (RoundedSystemTime 3600) AFTER the three
// FileInfo tokens (sndKey, size, digest); the parser surfaces it on the
// recipient rows (v1.6.17: it used to read the file SIZE as the time,
// dating every file into January 1970).
func TestParseXFTPStoreLogCreatedAt(t *testing.T) {
        log := "FNEW sId1 key1 102400 dig1 1761583200 active\n" +
                "FADD sId1 rcp1:key1\n" +
                "FNEW sId2 key2 2048 dig2 garbage-time active\n" +
                "FADD sId2 rcp2:key2\n"
        rows := ParseStoreLog(KindXFTP, []byte(log))
        if len(rows) != 2 {
                t.Fatalf("rows = %d, want 2", len(rows))
        }
        for _, r := range rows {
                switch r.ID {
                case "rcp1":
                        if r.CreatedAt != "1761583200" {
                                t.Errorf("rcp1 CreatedAt = %q, want 1761583200", r.CreatedAt)
                        }
                case "rcp2":
                        if r.CreatedAt != "" {
                                t.Errorf("rcp2 CreatedAt = %q, want empty for a non-numeric stamp", r.CreatedAt)
                        }
                }
        }
}

// TestNoteQueuesSeenStampsOnce: every newly listed ID gets exactly one
// first-seen stamp; existing stamps are never overwritten; a re-run with
// nothing new does not re-write the state file.
func TestNoteQueuesSeenStampsOnce(t *testing.T) {
        paths := testPaths(t)
        state, err := NewStateStore(paths)
        if err != nil {
                t.Fatal(err)
        }
        first := []QueueInfo{{ID: "q1"}, {ID: "q2"}}
        state.noteQueuesSeen(KindSMP, first)
        seen := state.ServerCopy(KindSMP).SeenQueues
        if len(seen) != 2 || seen["q1"] == "" || seen["q2"] == "" {
                t.Fatalf("after first run: %v", seen)
        }
        stamp1 := seen["q1"]

        // second run: q1 already known, q3 new - q1's stamp must survive
        state.noteQueuesSeen(KindSMP, []QueueInfo{{ID: "q1"}, {ID: "q3"}})
        seen = state.ServerCopy(KindSMP).SeenQueues
        if seen["q1"] != stamp1 {
                t.Fatalf("q1 stamp changed: %q -> %q", stamp1, seen["q1"])
        }
        if seen["q3"] == "" {
                t.Fatal("q3 not stamped")
        }

        // nothing new -> no re-write (mtime unchanged)
        before := statModTime(t, paths.StateFile())
        state.noteQueuesSeen(KindSMP, []QueueInfo{{ID: "q1"}, {ID: "q3"}})
        after := statModTime(t, paths.StateFile())
        if !before.Equal(after) {
                t.Fatal("state file re-written although nothing was new")
        }
}

func statModTime(t *testing.T, path string) time.Time {
        t.Helper()
        fi, err := os.Stat(path)
        if err != nil {
                t.Fatal(err)
        }
        return fi.ModTime()
}

// TestListQueuesFillsSeenAt: the SMP store log carries no timestamps, so
// ListQueues stamps the IDs and returns the first-seen time on each row.
func TestListQueuesFillsSeenAt(t *testing.T) {
        eng, paths, _ := queuesTestEngine(t, KindSMP, nil)
        logPath := filepath.Join(paths.LogDir(KindSMP), StoreLogName(KindSMP))
        log := "CREATE rid=r1 rk=k1 rdh=d1 sid=q1 sk=s2\n"
        if err := os.MkdirAll(filepath.Dir(logPath), 0o700); err != nil {
                t.Fatal(err)
        }
        if err := os.WriteFile(logPath, []byte(log), 0o600); err != nil {
                t.Fatal(err)
        }
        queues, err := ListQueues(eng, KindSMP)
        if err != nil {
                t.Fatal(err)
        }
        if len(queues) != 1 || queues[0].ID != "q1" {
                t.Fatalf("queues = %+v", queues)
        }
        if queues[0].SeenAt == "" {
                t.Fatal("SeenAt not filled for a chat queue")
        }
        if _, err := time.Parse(time.RFC3339, queues[0].SeenAt); err != nil {
                t.Fatalf("SeenAt %q is not RFC3339: %v", queues[0].SeenAt, err)
        }
}

// ---- v1.6.12: one-time connection links (LINK / LDELETE records) ----

// TestParseSMPStoreLogLinks: LINK records attach the one-time connection
// link to its recipient's queue (both are valid moderation targets - the
// servers resolve link IDs to the queue); LDELETE marks the link consumed.
func TestParseSMPStoreLogLinks(t *testing.T) {
        log := "CREATE rid=r1 rk=k1 rdh=d1 sid=q1 sk=s2\n" + // no link
                "CREATE rid=r2 rk=k1 rdh=d1 sid=q2 sk=s2\n" +
                "LINK r2 lnk2AAAAAAAA data-blob\n" + // live one-time link
                "CREATE rid=r3 rk=k1 rdh=d1 sid=q3 sk=s2\n" +
                "LINK r3 lnk3AAAAAAAA data-blob\n" +
                "LDELETE r3\n" + // consumed: connection established
                "CREATE rid=r4 rk=k1 rdh=d1 sid=q4 sk=s2 status=blocked,reason=spam\n" +
                "LINK r4 lnk4AAAAAAAA data-blob\n" + // live link on a banned queue
                "LINK r-garbage\n" // malformed: no link ID token
        rows := ParseStoreLog(KindSMP, []byte(log))
        byID := map[string]QueueInfo{}
        for _, r := range rows {
                byID[r.ID] = r
        }
        if len(rows) != 4 {
                t.Fatalf("rows = %d, want 4: %+v", len(rows), rows)
        }
        q1 := byID["q1"]
        if q1.LinkID != "" || q1.LinkLive {
                t.Errorf("q1 (contact-address queue) should carry no link: %+v", q1)
        }
        q2 := byID["q2"]
        if q2.LinkID != "lnk2AAAAAAAA" || !q2.LinkLive {
                t.Errorf("q2 should carry its live one-time link: %+v", q2)
        }
        q3 := byID["q3"]
        if q3.LinkLive {
                t.Errorf("q3's link was consumed (LDELETE) and must not be live: %+v", q3)
        }
        q4 := byID["q4"]
        if !q4.Blocked || !q4.LinkLive {
                t.Errorf("q4 should be banned with a live link (banning kills the code): %+v", q4)
        }
}

// TestBanByLinkID: blocking, checking, unblocking and deleting by a
// one-time LINK ID all resolve to the queue (the servers' getSenderQueue
// SSenderLink fallback) - banning a queue via its link kills the
// connection code.
func TestBanByLinkID(t *testing.T) {
        const sid = "SIDLINKTEST0001"
        const linkID = "LNKLINKTEST0001"
        f := newFakeControlServer(t, true).addQueue(sid).addLink(linkID, sid)
        rec := &ServerRec{ControlPort: f.port(), ControlAdmin: "tok"}

        out, err := BanClient(KindSMP, rec, linkID, BanReasonSpam, NewClientNotice(86400))
        if err != nil {
                t.Fatalf("ban by link ID failed: %v (out=%q)", err, out)
        }
        if !strings.Contains(f.wireLog(), "block "+linkID+" reason=spam") {
                t.Fatalf("wire command should carry the link ID: %q", f.wireLog())
        }
        if !f.blockedNow(sid) {
                t.Fatal("blocking by link ID should block the queue")
        }
        out, err = ClientStatus(KindSMP, rec, linkID)
        if err != nil || !strings.Contains(out, "EntityBlocked") {
                t.Fatalf("status by link ID -> err=%v out=%q", err, out)
        }
        out, err = UnbanClient(KindSMP, rec, linkID)
        if err != nil {
                t.Fatalf("unban by link ID failed: %v", err)
        }
        if f.blockedNow(sid) {
                t.Fatal("unban by link ID should unblock the queue")
        }
        if _, err = DeleteClient(KindSMP, rec, linkID); err != nil {
                t.Fatalf("delete by link ID failed: %v", err)
        }
}

// ---- v1.6.12: ban-target address parsing ----

// TestParseTargetID: the ban form accepts a bare queue / link / file ID
// or one of the https link forms (see ParseTargetIDShape). v1.6.22: bare
// smp:// / xftp:// server addresses are NOT accepted - smp:// is what
// the apps put in server settings, not a ban input - and neither is the
// compact simplex:/ in-app form (v1.6.28: still rejected, unmentioned).
func TestParseTargetID(t *testing.T) {
        qid := "LcJUMfVhwD8yxjAiSaDzzGF3-kLG4Uh0Fl_ZIjrRwjI"

        cases := []struct {
                name string
                in   string
                want string
        }{
                {"smp server address is not a ban target", "smp://" + qid + ":kEy@host.example.com:5223", ""},
                {"smp server address no port", "smp://" + qid + ":kEy@host.example.com", ""},
                {"xftp server address is not a ban target", "xftp://" + qid + ":kEy@host.example.com/ZmlsZS1kZXNjcmlwdGlvbg", ""},
                {"compact simplex:/ group link derives its ID", "simplex:/g#" + qid, "Zfq4GTzixVwEUBkNxiNs8WxBoUMJKaf5"},
                {"bare id", qid, qid},
                {"bare id with whitespace", "  " + qid + "  ", qid},
                {"address embedded in report text is not a ban target", "abuse report: user smp://" + qid + ":kEy@host.example.com is spamming", ""},
                {"https invitation link", "https://simplex.chat/contact#/?data=pasted-garbage", ""},
                {"notes without an ID", "my notes here", ""},
                {"too short", "abc123", ""},
                {"empty", "\n \n", ""},
        }
        for _, c := range cases {
                if got := ParseTargetID(c.in); got != c.want {
                        t.Errorf("%s: ParseTargetID = %q, want %q", c.name, got, c.want)
                }
        }
}

// ---- v1.6.11: the official ban notice (wire format) ----

// TestBanNoticeRoundTrip: banning with the official ban notice sends the
// exact wire bytes the servers' CPBlock grammar parses - reason plus
// ,notice={"ttl":...} - and the blocked status / unban responses carry
// the notice back, exactly as the real servers print it.
func TestBanNoticeRoundTrip(t *testing.T) {
        f := newFakeControlServer(t, true).addQueue("SIDNOTICETEST01")
        rec := &ServerRec{ControlPort: f.port(), ControlAdmin: "tok"}
        const sid = "SIDNOTICETEST01"

        // no-expiry notice: {"ttl":null} on the wire, ttl = Nothing in Show
        out, err := BanClient(KindSMP, rec, sid, BanReasonSpam, NewClientNotice(0))
        if err != nil {
                t.Fatalf("ban with notice failed: %v (out=%q)", err, out)
        }
        if !strings.Contains(f.wireLog(), `block SIDNOTICETEST01 reason=spam,notice={"ttl":null}`) {
                t.Fatalf("wire command wrong: %q", f.wireLog())
        }
        out, _ = ClientStatus(KindSMP, rec, sid)
        if !strings.Contains(out, "Just (ClientNotice {ttl = Nothing})") {
                t.Fatalf("status should show the no-expiry notice: %q", out)
        }

        // unban reports the stored notice, then a timed notice round-trips
        out, err = UnbanClient(KindSMP, rec, sid)
        if err != nil {
                t.Fatalf("unban failed: %v", err)
        }
        if !strings.Contains(out, "notice = Just (ClientNotice {ttl = Nothing})") {
                t.Fatalf("unban should echo the notice: %q", out)
        }
        out, err = BanClient(KindSMP, rec, sid, BanReasonContent, NewClientNotice(86400))
        if err != nil {
                t.Fatalf("ban with 1-day notice failed: %v", err)
        }
        if !strings.Contains(f.wireLog(), `block SIDNOTICETEST01 reason=content,notice={"ttl":86400}`) {
                t.Fatalf("1-day wire command wrong: %q", f.wireLog())
        }
        out, _ = ClientStatus(KindSMP, rec, sid)
        if !strings.Contains(out, "Just (ClientNotice {ttl = Just 86400})") {
                t.Fatalf("status should show the 1-day notice: %q", out)
        }
}

// TestClientNoticeJSON: the official simplexmq ClientNotice renders as
// exactly the JSON the servers' BlockingInfo parser decodes - {"ttl":null}
// for an indefinite ban or {"ttl":<seconds>} for a timed one - and never
// contains spaces (the control port splits commands on spaces).
func TestClientNoticeJSON(t *testing.T) {
        var nilNotice *ClientNotice
        if nilNotice.String() != "" {
                t.Fatalf("nil notice renders %q, want empty", nilNotice.String())
        }
        if got := NewClientNotice(0).String(); got != `{"ttl":null}` {
                t.Fatalf("no-expiry notice = %q, want {\"ttl\":null}", got)
        }
        if got := NewClientNotice(86400).String(); got != `{"ttl":86400}` {
                t.Fatalf("1-day notice = %q, want {\"ttl\":86400}", got)
        }
        for _, s := range []string{NewClientNotice(0).String(), NewClientNotice(604800).String()} {
                if strings.ContainsAny(s, " \t") {
                        t.Fatalf("notice %q contains whitespace", s)
                }
        }
}

// TestBlockingInfoArgWithNotice: the block command argument is exactly
// what the server parser accepts: reason=spam|content, optionally
// followed by ,notice=<json>.
func TestBlockingInfoArgWithNotice(t *testing.T) {
        cases := []struct {
                reason string
                notice *ClientNotice
                want   string
        }{
                {"", nil, "reason=spam"},
                {"content", nil, "reason=content"},
                {"free text", nil, "reason=spam"},
                {"", NewClientNotice(0), `reason=spam,notice={"ttl":null}`},
                {"content", NewClientNotice(86400), `reason=content,notice={"ttl":86400}`},
                {"spam", NewClientNotice(2592000), `reason=spam,notice={"ttl":2592000}`},
        }
        for _, c := range cases {
                if got := blockingInfoArg(c.reason, c.notice); got != c.want {
                        t.Errorf("blockingInfoArg(%q, %s) = %q, want %q", c.reason, c.notice, got, c.want)
                }
        }
}
