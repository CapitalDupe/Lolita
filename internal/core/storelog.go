package core

import (
        "fmt"
        "os"
        "path/filepath"
        "sort"
        "strconv"
        "strings"
)

// StoreLogName is the server store log file name for a kind. The store log
// journals every queue (SMP) / file (XFTP) the server ever created, with
// the IDs needed for moderation (block/delete/status) - it is the only
// place an operator can discover those IDs (verified against simplexmq
// v6.5.0 Server/StoreLog.hs and FileTransfer/Server/StoreLog.hs).
func StoreLogName(k Kind) string {
        switch k {
        case KindSMP:
                return "smp-server-store.log"
        case KindXFTP:
                return "file-server-store.log"
        case KindNTF:
                return "ntf-server-store.log"
        }
        return ""
}

// QueueInfo is one live queue (SMP chat queue) or file (XFTP) known from
// the server's store log.
type QueueInfo struct {
        ID      string // senderId (SMP) or fileId (XFTP): the ban target
        Blocked bool
        // CreatedAt is the server-reported creation time as epoch seconds
        // (XFTP FNEW records carry it, rounded to the hour; "" when the
        // log has none). The SMP store log carries NO queue timestamps at
        // all - ListQueues fills SeenAt instead.
        CreatedAt string
        // FileSize is the XFTP file size in bytes (decimal, from the FNEW
        // record; "" for SMP queues and shadow entries) - with the time
        // column it identifies which file a report refers to.
        FileSize string
        // FileID is the XFTP file's own record ID (the FNEW sender ID the
        // FADD recipient rows belong to; "" for SMP). The stored chunk
        // file on disk is named exactly after it, so it locates the file
        // for its precise upload time (noteUploadTimes).
        FileID string
        // UploadedAt is the XFTP file's precise upload time as epoch
        // seconds (v1.6.27): the modification time of the stored chunk
        // file, read from disk by noteUploadTimes - the journal's own
        // FNEW time is rounded to the hour, but the chunk file on disk
        // carries the real completion time to the second. "" when the
        // chunk file is gone (blocked and deleted files lose theirs) -
        // CreatedAt is the hour-precision fallback.
        UploadedAt string
        // SeenAt is when sxops first listed this ID (RFC3339) - the only
        // per-queue timestamp available for SMP queues.
        SeenAt string
        // LinkID is the connection link ID of this queue when the store log
        // carries a genuine one (v1.6.31): either a separate LINK record (the
        // LSET command - the app sends it only for contact connections, so
        // every LINK record is a live reusable link) or a link_id= field in a
        // CREATE record with queue_mode=C (the modern reusable contact /
        // channel / group / relay short-link flow - queues created WITH their
        // link; verified against simplexmq Agent.hs prepareLinkData: all the
        // reusable link kinds ride QRContact -> queue_mode=C). A link_id= on
        // a queue_mode=M queue is the leftover of a ONE-TIME invitation -
        // its link ID is a random server-generated ID, it is consumed by the
        // first join and never journaled as LDELETE, so it is NOT a live
        // link: that queue is an ordinary user's chat queue (v1.6.29-30
        // marked those rows "link" too, which mislabelled every one-time
        // invitation connection as a link). "" when the queue belongs to no
        // link. It is an equally valid moderation target: the servers
        // resolve link IDs to their queue via the SSenderLink fallback, and
        // banning the queue kills the connection code or address (its
        // LKEY/LGET then fail).
        LinkID string
        // LinkLive reports whether the link is still unconsumed (no LDELETE
        // record yet) - the invitation code or reusable address still works
        // until it is used / deleted or the queue is banned.
        LinkLive bool
        // BlockReason is the reason the server itself recorded with the
        // block ("spam" or "content" - the BlockingInfo wire format);
        // "" when the record carried none.
        BlockReason string
        // BlockNotice reports whether the server recorded the official ban
        // notice with the block; BlockNoticeTTL is its lifetime in seconds
        // (0 = no expiry - the null TTL).
        BlockNotice    bool
        BlockNoticeTTL int64
}

// ParseStoreLog reduces a store log tail to the current queue set.
//
// SMP record shapes (space separated tokens, ids are base64url;
// verified against simplexmq v6.5.0 and stable Server/StoreLog.hs):
//
//      CREATE rid=<rId> rk=.. rdh=.. sid=<senderId> sk=.. [queue_mode=<M|C>]
//            [link_id=<id> queue_data=..] [status=..]   <- queue_mode=C with
//            link_id: created WITH a reusable connection link (contact /
//            channel / group / relay); queue_mode=M with link_id: one-time
//            invitation leftover (not a live link)
//      LINK <rId> <linkId> <data..>   <- contact connection short link set
//            with LSET (a live reusable link)
//      LDELETE <rId>                  <- link consumed / deleted
//      DELETE <rId>              <- tuple encoded, NO rid= prefix
//      BLOCK <rId> reason=spam|content[,notice=..]
//      UNBLOCK <rId>
//      SUSPEND <rId>
//
// XFTP record shapes (FileTransfer/Server/StoreLog.hs + Protocol.hs):
//
//      FNEW <sId> <sndKey> <size> <digest> <createdAt> [active|blocked,<info>|off]
//      FADD <sId> <rId>:<key>[,<rId>:<key>..]
//      FACK <rId>
//      FDEL <sId>
//      FBLK <sId> reason=..
//
// FileInfo strEncodes as the THREE space-separated tokens sndKey, size
// (decimal bytes) and digest, so <createdAt> is the fifth token - v1.6.16
// and earlier read the SIZE as the creation time, dating every file into
// January 1970. <createdAt> itself is epoch seconds rounded to the hour
// (RoundedSystemTime 3600 - simplexmq SystemTime.hs strEncodes the plain
// Int64 seconds). The trailing status token is optional on decode. The
// digest token between the size and the time is NOT captured (v1.6.27
// removed the by-hash moderation): the stored chunk file's own mtime -
// noteUploadTimes - is the precise per-file time sxops shows.
//
// ServerEntityStatus encodes as "active" / "blocked,<info>" / "off"
// (QueueStore.hs StrEncoding) - the status token in a restored CREATE
// line is prefixed "status=", in FNEW it is a bare trailing token.
//
// The moderation TARGETS differ by server: SMP blocks by senderId (the
// sid= of CREATE), while the XFTP control port looks files up via the
// recipients map ("getFile SFRecipient") - only RECIPIENT file ids are
// accepted there, so the XFTP list is built from FADD records.
// The last record for an ID wins (logs are append-only journals).
func ParseStoreLog(k Kind, data []byte) []QueueInfo {
        switch k {
        case KindSMP:
                return parseSMPStoreLog(string(data))
        case KindXFTP:
                return parseXFTPStoreLog(string(data))
        }
        return nil
}

// parseSMPStoreLog tracks queues by recipient ID and returns sender IDs
// (the IDs the "block" control command accepts), together with each
// queue's one-time connection link when the log carries one.
func parseSMPStoreLog(s string) []QueueInfo {
        type q struct {
                sid         string
                blocked     bool
                deleted     bool
                suspended   bool
                linkID      string
                linkLive    bool
                blockReason string
                blockNotice bool
                blockTTL    int64
                order       int
        }
        byRid := map[string]*q{}
        order := 0
        for _, line := range strings.Split(s, "\n") {
                line = strings.TrimSpace(line)
                if line == "" {
                        continue
                }
                fields := strings.Fields(line)
                if len(fields) == 0 {
                        continue
                }
                switch fields[0] {
                case "CREATE":
                        rid := tokenValue(fields, "rid=")
                        sid := tokenValue(fields, "sid=")
                        if rid == "" || sid == "" {
                                continue
                        }
                        qq := byRid[rid]
                        if qq == nil {
                                order++
                                qq = &q{order: order}
                                byRid[rid] = qq
                        }
                        qq.sid = sid
                        qq.deleted = false
                        // restored state: "status=blocked,reason=.." (the
                        // Haskell ServerEntityStatus StrEncoding; the old
                        // check for the literal "EntityBlocked" never
                        // matched the wire format). The token carries the
                        // full official blocking info - the server's own
                        // record of the ban.
                        if st := tokenValue(fields, "status="); st != "" {
                                qq.blocked = strings.HasPrefix(st, "blocked")
                                qq.blockReason, qq.blockNotice, qq.blockTTL = parseBlockingInfo(st)
                        } else {
                                qq.blocked = false
                                qq.blockReason, qq.blockNotice, qq.blockTTL = "", false, 0
                        }
                        // "link_id=<id> queue_data=.." - only a
                        // CONTACT-mode queue's link_id is a genuine live
                        // link (v1.6.31). Verified against simplexmq: every
                        // reusable link kind (contact address / channel /
                        // group / relay) is created via QRContact ->
                        // queue_mode=C carrying its derived link ID, and
                        // LSET (the LINK record) is likewise contact-only.
                        // A queue_mode=M queue's link_id is the leftover of
                        // a ONE-TIME invitation: the server generated that
                        // ID at random, the first join consumed the link,
                        // and no LDELETE follows - the queue is an ordinary
                        // user's chat queue and must not be marked "link"
                        // (the v1.6.29-30 bug: every one-time invitation
                        // connection showed as a link).
                        if lid := tokenValue(fields, "link_id="); lid != "" && createIsContactMode(fields) {
                                qq.linkID = lid
                                qq.linkLive = true
                        } else {
                                qq.linkID = ""
                                qq.linkLive = false
                        }
                case "LINK":
                        // "LINK <rId> <linkId> <data..>" - the recipient's
                        // contact connection short link, set with LSET
                        // (addQueueLinkData journals it). LSET is sent only
                        // for contact connections (Agent.hs
                        // setConnShortLinkAsync), so every LINK record is a
                        // genuine live reusable link;
                        // block/unblock/delete/status accept the link
                        // ID as the target via the SSenderLink fallback)
                        if len(fields) < 3 {
                                continue
                        }
                        qq := byRid[fields[1]]
                        if qq == nil {
                                order++
                                qq = &q{order: order}
                                byRid[fields[1]] = qq
                        }
                        qq.linkID = fields[2]
                        qq.linkLive = true
                case "LDELETE":
                        // "LDELETE <rId>" - the link was consumed or deleted
                        if qq := byRid[recordID(fields)]; qq != nil {
                                qq.linkLive = false
                        }
                case "DELETE", "BLOCK", "UNBLOCK", "SUSPEND":
                        // these records carry the recipient ID positionally
                        // ("BLOCK <rid> reason=..") - there is no rid= token
                        qq := byRid[recordID(fields)]
                        if qq == nil {
                                continue
                        }
                        switch fields[0] {
                        case "DELETE":
                                qq.deleted = true
                        case "BLOCK":
                                // "BLOCK <rId> reason=..[,notice=..]" - the
                                // server journals every control-port block
                                // synchronously with the full official
                                // blocking info; this journal IS the ban list
                                qq.blocked = true
                                if len(fields) >= 3 {
                                        qq.blockReason, qq.blockNotice, qq.blockTTL = parseBlockingInfo(fields[2])
                                }
                        case "UNBLOCK":
                                qq.blocked = false
                                qq.blockReason, qq.blockNotice, qq.blockTTL = "", false, 0
                        case "SUSPEND":
                                qq.suspended = true
                        }
                }
        }
        out := make([]QueueInfo, 0, len(byRid))
        ordered := make([]*q, 0, len(byRid))
        for _, qq := range byRid {
                ordered = append(ordered, qq)
        }
        // insertion order (approximate: journal order)
        for i := 0; i < len(ordered); i++ {
                for j := i + 1; j < len(ordered); j++ {
                        if ordered[j].order < ordered[i].order {
                                ordered[i], ordered[j] = ordered[j], ordered[i]
                        }
                }
        }
        for _, qq := range ordered {
                if qq.sid == "" || qq.deleted {
                        continue
                }
                out = append(out, QueueInfo{ID: qq.sid, Blocked: qq.blocked,
                        BlockReason: qq.blockReason, BlockNotice: qq.blockNotice, BlockNoticeTTL: qq.blockTTL,
                        LinkID: qq.linkID, LinkLive: qq.linkLive})
        }
        return out
}

// parseXFTPStoreLog returns one QueueInfo per live file RECIPIENT -
// the file IDs the "block"/"delete" control commands accept (the XFTP
// control port resolves files through the recipient map, so the FNEW
// sender ID is NOT a valid moderation target).
func parseXFTPStoreLog(s string) []QueueInfo {
        type file struct {
                blocked     bool
                deleted     bool
                order       int // file first-seen order
                createdAt   string
                size        string
                rcps        []string
                blockReason string
                blockNotice bool
                blockTTL    int64
        }
        byID := map[string]*file{}      // by SENDER file id
        rcpFile := map[string]string{}  // recipient id -> sender file id
        rcpOrder := map[string]int{}
        n := 0
        getFile := func(sId string) *file {
                f := byID[sId]
                if f == nil {
                        n++
                        f = &file{order: n}
                        byID[sId] = f
                }
                return f
        }
        for _, line := range strings.Split(s, "\n") {
                line = strings.TrimSpace(line)
                if line == "" {
                        continue
                }
                fields := strings.Fields(line)
                if len(fields) < 2 {
                        continue
                }
                sid := fields[1]
                switch fields[0] {
                case "FNEW":
                        f := getFile(sid)
                        f.deleted = false
                        ca, st := fnewTail(fields)
                        f.createdAt = ca
                        f.size = fnewSize(fields)
                        // trailing status token: "active", "off" (retired
                        // files are not moderation targets) or
                        // "blocked,<info>" with the full official blocking
                        // info (restored bans)
                        if st == "off" {
                                f.deleted = true
                                f.blocked = false
                        } else if strings.HasPrefix(st, "blocked") {
                                f.blocked = true
                                f.blockReason, f.blockNotice, f.blockTTL = parseBlockingInfo(st)
                        } else {
                                f.blocked = false
                                f.blockReason, f.blockNotice, f.blockTTL = "", false, 0
                        }
                case "FADD":
                        // recipients: "rId:key" pairs, comma separated (one field)
                        f := getFile(sid)
                        if len(fields) >= 3 {
                                for _, pair := range strings.Split(fields[2], ",") {
                                        if rId := recipientID(pair); rId != "" {
                                                if _, known := rcpFile[rId]; !known {
                                                        n++
                                                        rcpOrder[rId] = n
                                                        f.rcps = append(f.rcps, rId)
                                                }
                                                rcpFile[rId] = sid
                                        }
                                }
                        }
                case "FACK":
                        // the recipient downloaded the file - no longer a target
                        if sId, ok := rcpFile[fields[1]]; ok {
                                if f := byID[sId]; f != nil {
                                        keep := f.rcps[:0]
                                        for _, r := range f.rcps {
                                                if r != fields[1] {
                                                        keep = append(keep, r)
                                                }
                                        }
                                        f.rcps = keep
                                }
                                delete(rcpFile, fields[1])
                        }
                case "FDEL":
                        if f := byID[sid]; f != nil {
                                f.deleted = true
                        }
                case "FBLK":
                        // "FBLK <sId> reason=..[,notice=..]" - the file
                        // server's journal of the ban (written
                        // synchronously by the control-port block)
                        if f := byID[sid]; f != nil {
                                f.blocked = true
                                if len(fields) >= 3 {
                                        f.blockReason, f.blockNotice, f.blockTTL = parseBlockingInfo(fields[2])
                                }
                        }
                }
        }
        // one row per live recipient, in first-seen order
        rows := make([]QueueInfo, 0, len(rcpFile))
        for rId, sId := range rcpFile {
                f := byID[sId]
                if f == nil || f.deleted {
                        continue
                }
                rows = append(rows, QueueInfo{ID: rId, Blocked: f.blocked,
                        BlockReason: f.blockReason, BlockNotice: f.blockNotice, BlockNoticeTTL: f.blockTTL,
                        CreatedAt: f.createdAt, FileSize: f.size, FileID: sId})
        }
        sort.Slice(rows, func(i, j int) bool { return rcpOrder[rows[i].ID] < rcpOrder[rows[j].ID] })
        return rows
}

// recipientID extracts the recipient id from an "rId:key" pair.
func recipientID(pair string) string {
        if i := strings.Index(pair, ":"); i > 0 {
                return pair[:i]
        }
        return ""
}

// fnewTail splits an FNEW record's trailing fields into the creation
// time and the status token. The real record shape (upstream
// FileTransfer/Server/StoreLog.hs) is
//
//      FNEW <sId> <sndKey> <size> <digest> <createdAt> [active|blocked,<info>|off]
//
// so the creation time is the field just before the trailing status
// word - or the last field when no status token follows (it is optional
// on decode). "" when absent or not a number. The digest token between
// the size and the time is NOT captured: sxops shows no file hashes
// (v1.6.27 removed the by-hash moderation) - files are identified by
// their ID, upload time and size.
func fnewTail(fields []string) (createdAt, status string) {
        if len(fields) < 3 {
                return "", ""
        }
        if last := fields[len(fields)-1]; last == "active" || last == "off" || strings.HasPrefix(last, "blocked") {
                status = last
                fields = fields[:len(fields)-1]
        }
        if len(fields) >= 3 && allDigits(fields[len(fields)-1]) {
                createdAt = fields[len(fields)-1]
        }
        return createdAt, status
}

// fnewSize extracts the file size in bytes (decimal) from an FNEW
// record: fields[3], after the sId and sndKey tokens ("" when the
// record is too short or the token is not a number).
func fnewSize(fields []string) string {
        if len(fields) >= 4 && allDigits(fields[3]) {
                return fields[3]
        }
        return ""
}

// noteUploadTimes stamps every XFTP row with the file's precise upload
// time (v1.6.27): the stored chunk file's modification time, read from
// disk. The upstream server (FileTransfer/Server.hs receiveServerFile)
// stores each uploaded chunk at <filesPath>/<base64url sId> - the file
// name is exactly the FNEW sender ID token sxops already records in
// QueueInfo.FileID - and writes it once at upload completion, so its
// mtime is the real upload time to the second. The journal's own FNEW
// time is rounded to the hour (RoundedSystemTime 3600), which is too
// coarse to tell which of two files uploaded in the same hour a report
// refers to; the mtime fills that gap. Rows whose chunk file is gone
// (blocked files lose theirs, deleted ones too) keep "" and fall back
// to the hour-precision CreatedAt. Missing directory / stat errors are
// not errors here - they just leave the fallback in place. SMP rows are
// returned untouched.
func noteUploadTimes(filesDir string, rows []QueueInfo) {
        if filesDir == "" {
                return
        }
        seen := map[string]string{} // sId -> epoch seconds ("" = no file)
        for i := range rows {
                if rows[i].FileID == "" {
                        continue
                }
                at, ok := seen[rows[i].FileID]
                if !ok {
                        fi, err := os.Stat(filepath.Join(filesDir, rows[i].FileID))
                        if err == nil && !fi.IsDir() {
                                at = strconv.FormatInt(fi.ModTime().Unix(), 10)
                        }
                        seen[rows[i].FileID] = at
                }
                if at != "" {
                        rows[i].UploadedAt = at
                }
        }
}

// allDigits reports whether s is a non-empty run of ASCII digits.
func allDigits(s string) bool {
        if s == "" {
                return false
        }
        for _, r := range s {
                if r < '0' || r > '9' {
                        return false
                }
        }
        return true
}

// parseBlockingInfo extracts the reason and the official ban notice from
// a BlockingInfo store-log token - the single space-free token the
// servers journal with BLOCK / FBLK records and embed in restored
// "status=blocked,<info>" / FNEW "blocked,<info>" status tokens
// (Protocol.hs StrEncoding: "reason=<spam|content>[,notice=<json>]",
// the notice being the ClientNotice JSON {"ttl":null} or {"ttl":<sec>}).
func parseBlockingInfo(token string) (reason string, notice bool, ttl int64) {
        token = strings.TrimPrefix(token, "blocked,")
        if !strings.HasPrefix(token, "reason=") {
                return "", false, 0
        }
        token = strings.TrimPrefix(token, "reason=")
        reason = token
        if i := strings.Index(token, ",notice="); i >= 0 {
                reason = token[:i]
                js := token[i+len(",notice="):]
                if strings.HasPrefix(js, `{"ttl":`) {
                        notice = true
                        v := strings.TrimSuffix(js[len(`{"ttl":`):], "}")
                        if n, err := strconv.ParseInt(v, 10, 64); err == nil {
                                ttl = n
                        }
                }
        }
        return reason, notice, ttl
}

// fnewCreatedAt is retired (v1.6.17): the FNEW record carries the
// FileInfo as three separate tokens before the creation time; use
// fnewTail / fnewSize instead.

// recordID returns a moderation record's target ID: positional
// ("BLOCK <rid> ...") or - for robustness against format variants -
// a "rid=" token.
func recordID(fields []string) string {
        if len(fields) > 1 && !strings.Contains(fields[1], "=") {
                return fields[1]
        }
        return tokenValue(fields, "rid=")
}

// createIsContactMode reports whether a CREATE record's queue_mode=
// token says CONTACT ("C") - the mode every reusable connection link
// rides (simplexmq QueueInfo.hs: smpEncode QMContact = "C", QMMessaging =
// "M"; Agent.hs prepareLinkData builds QRContact for all the reusable
// link kinds and QRMessaging only for one-time invitations).
func createIsContactMode(fields []string) bool {
        return tokenValue(fields, "queue_mode=") == "C"
}

// tokenValue returns the value of the "prefix" token ("rid=", "sid=", ...)
// from space-separated fields.
func tokenValue(fields []string, prefix string) string {
        for _, f := range fields {
                if strings.HasPrefix(f, prefix) {
                        return strings.TrimPrefix(f, prefix)
                }
        }
        return ""
}

// ListQueues reads the store log via the backend and returns the live
// queues/files with their ban status and the best available timestamp
// (server-reported creation for XFTP files, sxops first-seen for chat
// queues - the SMP server records no queue creation times). XFTP rows
// are additionally stamped with the stored chunk file's mtime - the
// upload time to the second (v1.6.27) - via noteUploadTimes.
func ListQueues(e *Engine, k Kind) ([]QueueInfo, error) {
        if StoreLogName(k) == "" {
                return nil, fmt.Errorf("%s has no store log", Def(k).Name)
        }
        data, err := e.Backend.ReadStoreLog(k, 5000)
        if err != nil {
                return nil, err
        }
        queues := ParseStoreLog(k, data)
        // stamp newly discovered IDs so every row carries an identifying
        // date AND time (no-op + no disk write when nothing is new)
        e.State.noteQueuesSeen(k, queues)
        seen := e.State.ServerCopy(k).SeenQueues
        for i := range queues {
                queues[i].SeenAt = seen[queues[i].ID]
        }
        if k == KindXFTP {
                noteUploadTimes(e.Paths.FilesDir(), queues)
        }
        return queues, nil
}
