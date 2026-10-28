package core

import (
        "bytes"
        "encoding/base64"
        "strings"
        "testing"
)

// v1.6.24 (by-hash parts retired in v1.6.27): the parser handles the
// REAL server journal format, verified against a live xftp-server
// v6.5.0.17 (ba6af65):
//
//   - the control-port "block <id> reason=..." and "delete <id>" commands
//     resolve the id through the RECIPIENT map only (getFile SFRecipient),
//     so only LIVE RECIPIENT ids are valid targets - the FNEW sender id
//     answers "error: AUTH", and so does a recipient that already
//     downloaded (FACK removes it from the map);
//   - the server journals the SHA-256 digest in FNEW always with one "="
//     of base64url padding (Data.ByteString.Base64.URL U.encode - both
//     live appends and the compacted log written on restart) - the token
//     sits between the size and the creation time and is simply skipped
//     (v1.6.27 removed the by-hash moderation: no digest is captured).
//
// The log below is the real wire format: 32-char ids, 44-byte X509 SPKI
// keys and the 32-byte digest, all base64url WITH padding (the compacted
// log the server writes on restart).

func realFormatLog(dgPadded string) string {
        // genuine shapes from the live v6.5.0.17 test server (contents
        // re-randomized): sender key Ed25519 SPKI, recipient keys X25519
        // SPKI, one FADD with two comma-separated recipients
        const (
                SA  = "8rnTEOpVeRHgPUgN2cWXwSybaR5QjvSQ"
                KA  = "MCowBQYDK2VwAyEAc8WpPd4ucVfkg7HCnt4N790qpSxBPofp7v4HXp86WB8="
                RA1 = "WDpczpcNh1BCxhjPPD4_hztR0HL_BJp1"
                RK1 = "MCowBQYDK2VuAyEAqN6jz6GrSmOBf7SfAmD4YYGuwVxUQnd2YmMu30hXbjI="
                RA2 = "l63WmVKMFiU3PvED31iTkXcXndCcoF5d"
                RK2 = "MCowBQYDK2VuAyEABozAdO6rcANQUAKfY15XC5b2915LGh3CS5NVflB7GWg="
        )
        return "FNEW " + SA + " " + KA + " 102400 " + dgPadded + " 1792908000 active\n" +
                "FADD " + SA + " " + RA1 + ":" + RK1 + "," + RA2 + ":" + RK2 + "\n"
}

// TestXFTPRealJournalModerationTargets: only LIVE RECIPIENT ids are
// moderation targets (the control port resolves ids through the
// recipient map) - the sender id never appears as a row, and a
// recipient that downloaded (FACK) is gone, exactly like on the server.
// Every row carries the file's own record ID (FileID - the FNEW sender
// id, which is also the stored chunk file's name on disk) and the
// size/time recorded for it.
func TestXFTPRealJournalModerationTargets(t *testing.T) {
        dg := bytes.Repeat([]byte("fedcba9876543210"), 2)
        dgPadded := base64.URLEncoding.EncodeToString(dg)
        log := realFormatLog(dgPadded) +
                "FNEW h-Ylit-hPDvDbcn9kfttDlpFVcGN6J80 MCowBQYDK2VwAyEASHCNYCl54z-N1BUuC03Z6Jt0jaTvnQ6UvFKafM2t8Us= 204800 " +
                base64.URLEncoding.EncodeToString(bytes.Repeat([]byte("ab"), 32)) + " 1792908000 active\n" +
                "FADD h-Ylit-hPDvDbcn9kfttDlpFVcGN6J80 NaRwr8iQKH6DjnBSKAPRYK-96rxqBPTd:MCowBQYDK2VuAyEAy1d2nWStUPSwh-XUWkRVYjgsrOaJU3PGfP2ZRO17oFk=\n" +
                "FACK NaRwr8iQKH6DjnBSKAPRYK-96rxqBPTd\n"
        rows := ParseStoreLog(KindXFTP, []byte(log))
        byID := map[string]QueueInfo{}
        for _, r := range rows {
                byID[r.ID] = r
        }
        // the two live recipients of file A are targets...
        if _, ok := byID["WDpczpcNh1BCxhjPPD4_hztR0HL_BJp1"]; !ok {
                t.Errorf("live recipient RA1 missing from rows: %+v", rows)
        }
        if _, ok := byID["l63WmVKMFiU3PvED31iTkXcXndCcoF5d"]; !ok {
                t.Errorf("live recipient RA2 missing from rows: %+v", rows)
        }
        // ...and each carries its file's own record ID, size and time
        for _, r := range rows {
                if r.FileID != "8rnTEOpVeRHgPUgN2cWXwSybaR5QjvSQ" && r.FileID != "h-Ylit-hPDvDbcn9kfttDlpFVcGN6J80" {
                        t.Errorf("row %s: FileID = %q (not one of the FNEW sender ids)", r.ID, r.FileID)
                }
                if r.FileSize != "102400" && r.FileSize != "204800" {
                        t.Errorf("row %s: FileSize = %q", r.ID, r.FileSize)
                }
                if r.CreatedAt != "1792908000" {
                        t.Errorf("row %s: CreatedAt = %q, want 1792908000", r.ID, r.CreatedAt)
                }
        }
        // ...the sender id is not a target, and neither is the FACKed recipient
        for _, notTarget := range []string{
                "8rnTEOpVeRHgPUgN2cWXwSybaR5QjvSQ",  // FNEW sender id
                "h-Ylit-hPDvDbcn9kfttDlpFVcGN6J80",  // second file's sender id
                "NaRwr8iQKH6DjnBSKAPRYK-96rxqBPTd",  // downloaded (FACK) recipient
        } {
                if r, ok := byID[notTarget]; ok {
                        t.Errorf("non-target %q appears as a row (%+v) - the control port would answer error: AUTH", notTarget, r)
                }
        }
}

// TestXFTPRealJournalBlockedFile: after the official block the server
// journals "FBLK <sId> reason=..." (with the SENDER id) and, on the
// restart that follows, FNEW carries "blocked,<info>" as the status -
// both mark every recipient row of the file blocked.
func TestXFTPRealJournalBlockedFile(t *testing.T) {
        dg := bytes.Repeat([]byte{0x77}, 32)
        dgPadded := base64.URLEncoding.EncodeToString(dg)
        const SA = "8rnTEOpVeRHgPUgN2cWXwSybaR5QjvSQ"

        // live journal: FNEW active + the FBLK the control-port block wrote
        live := realFormatLog(dgPadded) + "FBLK " + SA + " reason=spam\n"
        for _, r := range ParseStoreLog(KindXFTP, []byte(live)) {
                if !r.Blocked || r.BlockReason != "spam" {
                        t.Errorf("after FBLK: row %s Blocked=%v reason=%q, want true/spam", r.ID, r.Blocked, r.BlockReason)
                }
        }

        // compacted journal (restart): FNEW status carries the blocking info
        compacted := strings.Replace(realFormatLog(dgPadded), " active\n", " blocked,reason=spam\n", 1)
        for _, r := range ParseStoreLog(KindXFTP, []byte(compacted)) {
                if !r.Blocked || r.BlockReason != "spam" {
                        t.Errorf("restored blocked file: row %s Blocked=%v reason=%q, want true/spam", r.ID, r.Blocked, r.BlockReason)
                }
        }
}
