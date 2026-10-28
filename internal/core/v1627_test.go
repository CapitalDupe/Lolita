package core

import (
        "os"
        "path/filepath"
        "strconv"
        "strings"
        "testing"
        "time"
)

// v1.6.27: the SHA-256 / by-hash file moderation is REMOVED - the file
// list is identified by file ID, upload time and size - and the file
// time column gained minute-and-second precision: the stored chunk
// file's own modification time, read from the XFTP files directory
// (the journal's FNEW time is rounded to the hour, too coarse to tell
// two uploads in the same hour apart).

// TestParseXFTPStoreLogFileID: every recipient row of a file carries
// the file's own record ID (the FNEW sender id - also the name of the
// stored chunk file on disk, which noteUploadTimes stats). The digest
// token the server journals between the size and the creation time is
// tolerated and simply not captured.
func TestParseXFTPStoreLogFileID(t *testing.T) {
        log := "FNEW S1 keyA 102400 dgAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA= 1761000000 active\n" +
                "FADD S1 R1:keyR1\n" +
                "FADD S1 R2:keyR2\n" +
                "FNEW S2 keyB 2048 digB 1761003600\n" +
                "FADD S2 R3:keyR3\n"
        rows := ParseStoreLog(KindXFTP, []byte(log))
        if len(rows) != 3 {
                t.Fatalf("rows = %d, want 3: %+v", len(rows), rows)
        }
        byID := map[string]QueueInfo{}
        for _, r := range rows {
                byID[r.ID] = r
        }
        for _, rID := range []string{"R1", "R2"} {
                if r := byID[rID]; r.FileID != "S1" {
                        t.Errorf("row %s: FileID = %q, want S1", rID, r.FileID)
                }
        }
        if r := byID["R3"]; r.FileID != "S2" {
                t.Errorf("row R3: FileID = %q, want S2", r.FileID)
        }
}

// TestNoteUploadTimes: the stored chunk file's mtime - the upload time
// to the second - stamps every recipient row of that file; rows whose
// chunk file is gone (blocked / deleted files lose theirs) keep "" and
// the hour-precision fallback. The upstream server stores each chunk at
// <filesPath>/<base64url sId> (FileTransfer/Server.hs receiveServerFile),
// i.e. the file name IS the FNEW sender id sxops records in FileID.
func TestNoteUploadTimes(t *testing.T) {
        dir := t.TempDir()
        want := time.Date(2026, 10, 24, 13, 47, 52, 0, time.UTC)
        name := "S1"
        if err := os.WriteFile(filepath.Join(dir, name), []byte("chunk"), 0o600); err != nil {
                t.Fatal(err)
        }
        if err := os.Chtimes(filepath.Join(dir, name), want, want); err != nil {
                t.Fatal(err)
        }

        rows := []QueueInfo{
                {ID: "R1", FileID: "S1", CreatedAt: "1761000000"},
                {ID: "R2", FileID: "S1", CreatedAt: "1761000000"},
                {ID: "R3", FileID: "S2", CreatedAt: "1761003600"}, // no chunk file on disk
                {ID: "Q1"},                                         // SMP row: untouched
        }
        noteUploadTimes(dir, rows)

        for i, rID := range []string{"R1", "R2"} {
                if rows[i].UploadedAt != strconv.FormatInt(want.Unix(), 10) {
                        t.Errorf("row %s: UploadedAt = %q, want %d", rID, rows[i].UploadedAt, want.Unix())
                }
        }
        if rows[2].UploadedAt != "" {
                t.Errorf("row R3 (chunk file gone): UploadedAt = %q, want \"\" (hour fallback)", rows[2].UploadedAt)
        }
        if rows[2].CreatedAt != "1761003600" {
                t.Errorf("row R3: CreatedAt = %q - the fallback must stay in place", rows[2].CreatedAt)
        }
        if rows[3].UploadedAt != "" || rows[3].FileID != "" {
                t.Errorf("SMP-looking row got stamped: %+v", rows[3])
        }

        // a missing files directory is not an error - the fallback stays
        empty := []QueueInfo{{ID: "R1", FileID: "S1", CreatedAt: "1761000000"}}
        noteUploadTimes(filepath.Join(dir, "no-such-dir"), empty)
        if empty[0].UploadedAt != "" {
                t.Errorf("missing dir: UploadedAt = %q, want \"\"", empty[0].UploadedAt)
        }
        // "" disables the lookup entirely
        noteUploadTimes("", empty)
        if empty[0].UploadedAt != "" {
                t.Errorf("empty dir: UploadedAt = %q, want \"\"", empty[0].UploadedAt)
        }

        // and the journal hour fallback still parses
        if !strings.Contains(rows[0].CreatedAt, "1761000000") {
                t.Errorf("CreatedAt mangled: %+v", rows[0])
        }
}
