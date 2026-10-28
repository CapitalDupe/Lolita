package core

import "testing"

// v1.6.17: the XFTP list showed every file "created" in January 1970.
// Root cause: the FNEW store-log record is
//
//      FNEW <sId> <sndKey> <size> <digest> <createdAt> [active|blocked,<info>|off]
//
// (FileInfo strEncodes as the three tokens sndKey, size, digest - see
// upstream FileTransfer/Server/StoreLog.hs + Protocol.hs), and the
// parser read fields[3] - the file SIZE - as the creation time. A
// 200 KB file became 200000 epoch seconds = 1970-01-03. The fix reads
// the creation time from before the trailing status token and now also
// surfaces the SIZE (fields[3]) as a per-row identification column.

// TestParseXFTPStoreLogRealFnew pins the real FNEW layout: the size is
// never the date, the date sits before the trailing status word, the
// status sits at the END of the line (not at the old fields[4]), and
// the size is surfaced on the recipient rows.
func TestParseXFTPStoreLogRealFnew(t *testing.T) {
        // the old bug in one line: a 1730000000-byte file (~1.7 GB) whose
        // SIZE looks exactly like an epoch timestamp - the date must be
        // the 1761000000 that follows the digest, not the size
        log := "FNEW S1 keyA 1730000000 digA 1761000000 active\n" +
                "FADD S1 R1:keyR1\n"
        rows := ParseStoreLog(KindXFTP, []byte(log))
        if len(rows) != 1 {
                t.Fatalf("rows = %d, want 1: %+v", len(rows), rows)
        }
        r := rows[0]
        if r.CreatedAt != "1761000000" {
                t.Errorf("CreatedAt = %q, want 1761000000 (the token after the digest, not the size)", r.CreatedAt)
        }
        if r.FileSize != "1730000000" {
                t.Errorf("FileSize = %q, want 1730000000 (the third token after the sId)", r.FileSize)
        }
        if r.Blocked {
                t.Errorf("S1 must be active: %+v", r)
        }
}

// TestParseXFTPStoreLogRealFnewStatuses: the trailing status token is
// detected wherever it sits at the END of the line - restored bans
// ("blocked,<info>"), retired files ("off") and the optional-token
// case (no status at all).
func TestParseXFTPStoreLogRealFnewStatuses(t *testing.T) {
        log := "FNEW S1 keyA 1000 digA 1761000000 blocked,reason=spam,notice={\"ttl\":172800}\n" + // restored ban
                "FADD S1 R1:keyR1\n" +
                "FNEW S2 keyB 2000 digB 1761003600 off\n" + // retired
                "FADD S2 R2:keyR2\n" +
                "FNEW S3 keyC 3000 digC 1761007200\n" + // status token optional
                "FADD S3 R3:keyR3\n"
        rows := ParseStoreLog(KindXFTP, []byte(log))
        byID := map[string]QueueInfo{}
        for _, r := range rows {
                byID[r.ID] = r
        }
        if len(rows) != 2 { // R2's file is retired
                t.Fatalf("rows = %d, want 2 (R1, R3): %+v", len(rows), rows)
        }
        a := byID["R1"]
        if !a.Blocked || a.BlockReason != "spam" || !a.BlockNotice || a.BlockNoticeTTL != 172800 {
                t.Errorf("restored blocked FNEW misparsed: %+v", a)
        }
        if a.CreatedAt != "1761000000" || a.FileSize != "1000" {
                t.Errorf("restored FNEW tail misparsed: created=%q size=%q", a.CreatedAt, a.FileSize)
        }
        c := byID["R3"]
        if c.Blocked {
                t.Errorf("missing status token must mean active: %+v", c)
        }
        if c.CreatedAt != "1761007200" || c.FileSize != "3000" {
                t.Errorf("status-less FNEW misparsed: created=%q size=%q", c.CreatedAt, c.FileSize)
        }
}

// TestFnewTailUnit pins the helper directly: short records, garbage
// tails and size-like timestamps.
func TestFnewTailUnit(t *testing.T) {
        // full record
        ca, st := fnewTail([]string{"FNEW", "S1", "keyA", "102400", "digA", "1761000000", "active"})
        if ca != "1761000000" || st != "active" {
                t.Errorf("full record: got (%q, %q), want (1761000000, active)", ca, st)
        }
        // no status token: the last field IS the creation time
        ca, st = fnewTail([]string{"FNEW", "S1", "keyA", "102400", "digA", "1761000000"})
        if ca != "1761000000" || st != "" {
                t.Errorf("no status: got (%q, %q), want (1761000000, \"\")", ca, st)
        }
        // retired
        _, st = fnewTail([]string{"FNEW", "S1", "keyA", "102400", "digA", "1761000000", "off"})
        if st != "off" {
                t.Errorf("off status: got %q", st)
        }
        // truncated records never panic, never invent a date
        ca, st = fnewTail([]string{"FNEW", "S1"})
        if ca != "" || st != "" {
                t.Errorf("truncated: got (%q, %q), want empty", ca, st)
        }
        // a digest-sized garbage tail is not a date
        ca, _ = fnewTail([]string{"FNEW", "S1", "keyA", "102400", "digA", "not-a-number"})
        if ca != "" {
                t.Errorf("garbage tail: got date %q, want empty", ca)
        }
        // size extraction: always the fourth token
        if s := fnewSize([]string{"FNEW", "S1", "keyA", "102400", "digA", "1761000000", "active"}); s != "102400" {
                t.Errorf("fnewSize full = %q, want 102400", s)
        }
        if s := fnewSize([]string{"FNEW", "S1", "keyA"}); s != "" {
                t.Errorf("fnewSize short = %q, want empty", s)
        }
}
