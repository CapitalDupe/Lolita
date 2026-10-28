package ui

import (
        "os"
        "reflect"
        "strings"
        "testing"

        "fyne.io/fyne/v2"
        "fyne.io/fyne/v2/test"
        "fyne.io/fyne/v2/widget"

        "sxops/internal/core"
)

// v1.6.21: the ban dialog ADAPTS TO WHAT IS GETTING BANNED. A pasted
// connection link (one-time invitation, contact / group / channel /
// relay link, any of its forms) opens the LINK ban (its own dialog
// since v1.6.32): the link block with its reason select and NO
// expiry, plus the delete of the queue behind the link (v1.6.33) -
// and NO queue-block option, because the link block is delivered
// through the official queue block anyway. Anything else (a queue ID, a full smp:// address, an
// unknown bare ID) opens the USER ban: the queue block, "Block link
// creation" (the official ban notice - the expiry select lives on this
// option, there is no reason row there) and the delete. The XFTP
// dialog is file-shaped regardless of the pasted text (files carry no
// links): the two official file methods - the block with the reason,
// and the delete. An upload-restriction option is deliberately absent
// there: the file server has no uploader identity to restrict (the
// official protocol has no such command - the RFC lists it as future,
// client-side voluntary work), and the upload password
// (create_password) is the operator's own settings lever, not
// something to attach to an abuse report.

// TestNoticeExpiryOptions: every offered expiry round-trips through
// noticeExpiryTTLOf, the removed "1 day" and the retired "queue only"
// stay absent, and "no expiry" maps to TTL 0 (ClientNotice
// {"ttl":null}).
func TestNoticeExpiryOptions(t *testing.T) {
        has1day, hasQueueOnly := false, false
        for _, o := range noticeExpiryOptions {
                if o == "1 day" {
                        has1day = true
                }
                if o == "queue only" {
                        hasQueueOnly = true
                }
                ttl, ok := noticeExpiryTTLOf(o)
                if !ok {
                        t.Fatalf("option %q must map to a TTL", o)
                }
                if ttl < 0 {
                        t.Fatalf("option %q mapped to a negative TTL", o)
                }
        }
        if has1day {
                t.Fatal("\"1 day\" must be removed from the notice expiry options")
        }
        if hasQueueOnly {
                t.Fatal("\"queue only\" must not be an expiry option (untick the check mark instead)")
        }
        if ttl, ok := noticeExpiryTTLOf("2 days"); !ok || ttl != 172800 {
                t.Fatalf("2 days must map to 172800 seconds, got %d/%v", ttl, ok)
        }
        if ttl, ok := noticeExpiryTTLOf("12 hours"); !ok || ttl != 43200 {
                t.Fatalf("12 hours must map to 43200 seconds, got %d/%v", ttl, ok)
        }
        if ttl, ok := noticeExpiryTTLOf("no expiry"); !ok || ttl != 0 {
                t.Fatalf("no expiry must map to TTL 0 (null), got %d/%v", ttl, ok)
        }
        if _, ok := noticeExpiryTTLOf("1 fortnight"); ok {
                t.Fatal("unknown labels must not map to a TTL")
        }
}

// TestNoticeExpiryUntil: the summary phrase for a notice TTL.
func TestNoticeExpiryUntil(t *testing.T) {
        cases := map[int64]string{
                0:        "until you Unban",
                43200:    "for 12 hours",
                172800:   "for 2 days",
                604800:   "for 7 days",
                2592000:  "for 30 days",
                31536000: "for 1 year",
                999:      "for 999 seconds",
                -1:       "until you Unban",
        }
        for ttl, want := range cases {
                if got := noticeExpiryUntil(ttl); got != want {
                        t.Errorf("noticeExpiryUntil(%d) = %q, want %q", ttl, got, want)
                }
        }
}

// TestGateLine: the human summary of the server-wide registration gate.
func TestGateLine(t *testing.T) {
        s := gateLine(core.KindSMP, true)
        if !strings.Contains(s, "new_queues = off") || !strings.Contains(s, "refuses NEW registrations") {
                t.Fatalf("SMP blocked line: %q", s)
        }
        s = gateLine(core.KindXFTP, true)
        if !strings.Contains(s, "new_files = off") {
                t.Fatalf("XFTP blocked line: %q", s)
        }
        s = gateLine(core.KindSMP, false)
        if !strings.Contains(s, "new_queues = on") {
                t.Fatalf("SMP allowed line: %q", s)
        }
}

// TestShortBanTarget: dialog headline truncation of moderation targets.
func TestShortBanTarget(t *testing.T) {
        if shortBanTarget("abc") != "abc" {
                t.Fatal("short IDs stay as they are")
        }
        long := strings.Repeat("x", 40)
        s := shortBanTarget(long)
        runes := []rune(s)
        if len(runes) != 25 || !strings.HasSuffix(s, "…") || !strings.HasPrefix(s, "xxxxxxxx") {
                t.Fatalf("long IDs truncate to 25 runes with ellipsis, got %q", s)
        }
}

// TestBanChoices: the restriction list semantics - nothing ticked by
// default (Ban disabled) and every option independently tickable; the
// notice belongs to the user ban's "Block link creation" option, the
// link block to the link ban.
func TestBanChoices(t *testing.T) {
        // v1.6.16 default: NOTHING ticked - the dialog opens with the Ban
        // button disabled and the "tick at least one restriction" note
        def := banChoices{reason: core.BanReasonSpam, noticeTTL: 172800}
        if def.valid() {
                t.Fatal("the unticked default must be invalid (nothing chosen yet, Ban disabled)")
        }
        if def.withNotice() {
                t.Fatal("the unticked default must not send a notice")
        }
        // block link creation WITHOUT the queue block - a valid, executable
        // choice (sxops sends the official block command that carries it)
        linkCreationOnly := banChoices{linkCreation: true, noticeTTL: 604800}
        if !linkCreationOnly.valid() {
                t.Fatal("block link creation alone must be valid (the block command delivers it)")
        }
        if !linkCreationOnly.withNotice() {
                t.Fatal("block link creation must send the notice")
        }
        // block without the notice: the plain official queue block
        blockOnly := banChoices{block: true, reason: core.BanReasonContent}
        if !blockOnly.valid() || blockOnly.withNotice() {
                t.Fatal("block only must be valid without a notice")
        }
        // the link block (a link-ban restriction): no notice rides it
        linkOnly := banChoices{link: true, reason: core.BanReasonContent}
        if !linkOnly.valid() || linkOnly.withNotice() {
                t.Fatal("the link block alone must be valid without a notice")
        }
        // delete alone or with everything: valid
        delOnly := banChoices{del: true}
        if !delOnly.valid() || delOnly.withNotice() {
                t.Fatal("delete only must be valid without a notice")
        }
        all := banChoices{block: true, linkCreation: true, del: true, noticeTTL: 172800}
        if !all.valid() || !all.withNotice() {
                t.Fatal("all user restrictions ticked must be valid with a notice")
        }
}

// TestBanRestrictionSummary: the dialog's live "What happens to ..."
// list renders each ticked restriction, numbered, and follows the
// target's shape (v1.6.21; v1.6.32): a user ban lists block queue /
// block link creation (with the expiry phrase) / delete; a link ban
// lists ONLY the link block - one restriction, no delete line (the
// link dialog offers nothing else); on XFTP only the official file
// methods render.
func TestBanRestrictionSummary(t *testing.T) {
        // all three user restrictions on (SMP)
        s := banRestrictionSummary(banChoices{block: true, linkCreation: true, noticeTTL: 172800, del: true}, core.KindSMP, core.ShapeBareID)
        if !strings.Contains(s, "1. Block the queue") || !strings.Contains(s, "2. Block link creation") ||
                !strings.Contains(s, "for 2 days") ||
                !strings.Contains(s, "3. Delete the queue and its undelivered messages permanently") {
                t.Fatalf("SMP all-on user summary: %q", s)
        }
        // block only, no link-creation block
        s = banRestrictionSummary(banChoices{block: true}, core.KindSMP, core.ShapeBareID)
        if strings.Contains(s, "Block link creation") || strings.Contains(s, "Delete") {
                t.Fatalf("SMP block-only summary must not mention the link-creation block/delete: %q", s)
        }
        // block link creation ALONE - a full restriction in its own right,
        // with the queue-block delivery spelled out
        s = banRestrictionSummary(banChoices{linkCreation: true, noticeTTL: 0}, core.KindSMP, core.ShapeBareID)
        if !strings.HasPrefix(s, "1. Block link creation - their app refuses to create new links") ||
                !strings.Contains(s, "until you Unban") || !strings.Contains(s, "riding the queue block") {
                t.Fatalf("SMP link-creation-only summary: %q", s)
        }
        // delete only: numbered from 1
        s = banRestrictionSummary(banChoices{del: true}, core.KindSMP, core.ShapeBareID)
        if !strings.HasPrefix(s, "1. Delete the queue") {
                t.Fatalf("delete-only summary must be step 1: %q", s)
        }
        // LINK ban (v1.6.33): the block, plus the delete when it is
        // ticked - the delete line names the queue behind the link
        s = banRestrictionSummary(banChoices{link: true}, core.KindSMP, core.ShapeLink)
        if !strings.Contains(s, "1. Block the link") || strings.Contains(s, "2.") {
                t.Fatalf("SMP link-ban block-only summary must be the single block line: %q", s)
        }
        if strings.Contains(s, "Delete") {
                t.Fatalf("the link block-only summary must not list a delete: %q", s)
        }
        s = banRestrictionSummary(banChoices{link: true, del: true}, core.KindSMP, core.ShapeLink)
        if !strings.Contains(s, "1. Block the link") || !strings.Contains(s, "2. Delete the queue behind the link") {
                t.Fatalf("SMP link block + delete summary must list both lines: %q", s)
        }
        s = banRestrictionSummary(banChoices{del: true}, core.KindSMP, core.ShapeLink)
        if !strings.HasPrefix(s, "1. Delete the queue behind the link") || strings.Contains(s, "Block the link") {
                t.Fatalf("SMP link delete-only summary must be the delete line alone: %q", s)
        }
        s = banRestrictionSummary(banChoices{block: true, link: true}, core.KindSMP, core.ShapeLink)
        if strings.Contains(s, "Block the queue") || strings.Contains(s, "Delete") {
                t.Fatalf("a stray block flag on a link ban must not render a queue-block or delete line: %q", s)
        }
        // link-creation is not a link-ban option either
        s = banRestrictionSummary(banChoices{linkCreation: true, noticeTTL: 172800}, core.KindSMP, core.ShapeLink)
        if strings.Contains(s, "Block link creation") {
                t.Fatalf("the link ban must not render the link-creation option: %q", s)
        }
        // XFTP (v1.6.20): only the official methods - the block line
        // carries the reason; a notice choice does not exist there and
        // must not leak any upload-refusal wording
        s = banRestrictionSummary(banChoices{block: true, linkCreation: true}, core.KindXFTP, core.ShapeBareID)
        if !strings.Contains(s, "Block the file") {
                t.Fatalf("XFTP block summary: %q", s)
        }
        s = banRestrictionSummary(banChoices{block: true}, core.KindXFTP, core.ShapeBareID)
        if !strings.HasPrefix(s, "1. Block the file") {
                t.Fatalf("XFTP block-only summary: %q", s)
        }
        // a link-creation-only choice renders nothing on the file server
        s = banRestrictionSummary(banChoices{linkCreation: true, noticeTTL: 172800}, core.KindXFTP, core.ShapeLink)
        if s != "" {
                t.Fatalf("XFTP has no notice or link restriction - the summary must be empty: %q", s)
        }
        // nothing chosen: empty summary
        s = banRestrictionSummary(banChoices{}, core.KindSMP, core.ShapeBareID)
        if s != "" {
                t.Fatalf("empty choices must render an empty summary, got %q", s)
        }
}

// TestHumanSize: the v1.6.17 XFTP size column formatting - sizes come
// from the FNEW record as decimal bytes.
func TestHumanSize(t *testing.T) {
        cases := map[string]string{
                "0":          "0 B",
                "731":        "731 B",
                "1024":       "1.0 KB",
                "204800":     "200.0 KB",
                "3355443":    "3.2 MB",
                "1730000000": "1.6 GB",
                "":           "",
                "garbage":    "",
        }
        for in, want := range cases {
                if got := humanSize(in); got != want {
                        t.Errorf("humanSize(%q) = %q, want %q", in, got, want)
                }
        }
}

// TestBanCardCopyXFTPCorrectSemantics (v1.6.18): the XFTP moderation
// copy must state the verified upstream semantics - the block DELETES
// the stored chunks at block time, there is no unblock, and no daily
// quota on the file server - and must NOT carry the old wrong claims.
func TestBanCardCopyXFTPCorrectSemantics(t *testing.T) {
        c := banCardCopy(core.KindXFTP)
        if c == "" {
                t.Fatal("XFTP ban card copy is empty")
        }
        if !strings.Contains(c, "chunks are removed") {
                t.Errorf("the copy must say the chunks are removed at block time: %q", c)
        }
        if !strings.Contains(c, "BLOCKED error") {
                t.Errorf("the copy must mention the BLOCKED error clients see: %q", c)
        }
        if !strings.Contains(c, "cannot be undone") {
                t.Errorf("the copy must say it cannot be undone: %q", c)
        }
        if strings.Contains(c, "only deleting the file removes it") {
                t.Errorf("the old wrong claim must be gone: %q", c)
        }
        if strings.Contains(c, "about 20 bans per day.\\nEvery ban") {
                t.Errorf("the SMP quota sentence must not bleed into the XFTP copy: %q", c)
        }
        // v1.6.20: no upload-blocking claim (not an official method), and
        // the copy says so explicitly; the upload password is NOT a ban
        // option (v1.6.21 removed it after review - it is the operator's
        // own settings lever, not something to attach to an abuse report)
        if strings.Contains(c, "block file uploads") || strings.Contains(c, "refuses further uploads") {
                t.Errorf("the copy must not claim upload blocking (not an official method): %q", c)
        }
        if !strings.Contains(c, "no way to block future uploads") {
                t.Errorf("the copy must say there is no way to block future uploads: %q", c)
        }
        if strings.Contains(c, "upload password") {
                t.Errorf("the XFTP ban copy must not advertise the upload password (it is not a moderation command): %q", c)
        }
        if !strings.Contains(c, "Deleting the file is the other official method") {
                t.Errorf("the copy must present delete as the other official method: %q", c)
        }
        // v1.6.27: the by-hash flow is REMOVED together with every
        // SHA-256 reference - the field takes file IDs only, and the
        // report flow matches rows by upload time and size
        if strings.Contains(c, "SHA-256") || strings.Contains(c, "content hash") || strings.Contains(c, "by-hash") {
                t.Errorf("the by-hash ban must be gone from the copy: %q", c)
        }
        if !strings.Contains(c, "find the matching rows in the file list above by upload time and size") {
                t.Errorf("the report flow must match rows by upload time and size (list is ABOVE the field): %q", c)
        }
}

// TestBanCardCopySMPAdaptiveDialog (v1.6.28): the SMP card copy
// describes both target flows - the user ban with "block the queue"
// and "block link creation", the link ban with the link block and its
// reason - in its slimmed, https-only wording: no version
// self-reference and no compact-scheme mention.
func TestBanCardCopySMPAdaptiveDialog(t *testing.T) {
        s := banCardCopy(core.KindSMP)
        if !strings.Contains(s, "Banning is the official SimpleX moderation method") {
                t.Errorf("SMP copy lost its opening: %q", s)
        }
        if !strings.Contains(s, "block the queue") {
                t.Errorf("SMP copy must name the user-ban option \"block the queue\": %q", s)
        }
        if !strings.Contains(s, "block link creation") {
                t.Errorf("SMP copy must name the user-ban option \"block link creation\": %q", s)
        }
        if !strings.Contains(s, "Banning a pasted link") {
                t.Errorf("SMP copy must describe the pasted-link flow: %q", s)
        }
        if strings.Contains(s, "block the connection to that link - if the queue carries a live invitation") {
                t.Errorf("the old notice-option wording must be gone: %q", s)
        }
        if strings.Contains(s, "chunks are removed") {
                t.Errorf("SMP copy must not use file wording: %q", s)
        }
        // v1.6.20: the report flow carries no smp:// addresses
        if !strings.Contains(s, "carry no smp:// addresses") {
                t.Errorf("SMP copy must say complaints carry no smp:// addresses: %q", s)
        }
        if strings.Contains(s, "a full address (smp://…)") {
                t.Errorf("SMP copy must not advertise pasting smp:// addresses: %q", s)
        }
        // v1.6.22: the accepted link forms are the https forms; the
        // copy explains that smp:// addresses are not ban inputs
        // (v1.6.31: the compact simplex:/ forms are accepted again, and
        // the copy describes both spellings)
        if !strings.Contains(s, "in either of its forms") {
                t.Errorf("SMP copy must describe both link spellings: %q", s)
        }
        if !strings.Contains(s, "simplex:/") {
                t.Errorf("SMP copy must mention the compact simplex:/ form: %q", s)
        }
        if !strings.Contains(s, "are not ban inputs") {
                t.Errorf("SMP copy must say the rejected forms are not ban inputs: %q", s)
        }
        if !strings.Contains(s, "server address") {
                t.Errorf("SMP copy must explain smp:// is a server address: %q", s)
        }
        // v1.6.31: the compact scheme is referenced again (it is an
        // accepted form now); the v1.6.28-era requirement that it NOT
        // appear is reverted with the feature itself
        if strings.Contains(s, "compact simplex:/ form, its https server form") {
                t.Errorf("the old advertisement of the simplex:/ forms must be gone: %q", s)
        }
        if strings.Contains(s, "The field accepts a queue ID, a connection link ID") {
                t.Errorf("the old field-accepts sentence must be gone: %q", s)
        }
}

// TestBanActionAndTargetWords (v1.6.18 + v1.6.21 + v1.6.33): the
// dialog verbs name what each server's moderation actually acts on -
// a queue or a link (Ban user / Ban link) on the chat server, a
// file (Block file) on the file server.
func TestBanActionAndTargetWords(t *testing.T) {
        if got := banActionWord(core.KindXFTP, core.ShapeBareID); got != "Block" {
                t.Errorf("XFTP action word = %q, want Block", got)
        }
        if got := banActionWord(core.KindSMP, core.ShapeBareID); got != "Ban" {
                t.Errorf("SMP user action word = %q, want Ban", got)
        }
        // v1.6.33: the link dialog offers the block AND the delete, so its
        // umbrella verb matches the user dialog's - the action-specific
        // verb lives on its confirm button (Block / Delete)
        if got := banActionWord(core.KindSMP, core.ShapeLink); got != "Ban" {
                t.Errorf("SMP link action word = %q, want Ban", got)
        }
        if got := banTargetWord(core.KindXFTP, core.ShapeLink); got != "file" {
                t.Errorf("XFTP target word = %q, want file (the file dialog ignores the shape)", got)
        }
        if got := banTargetWord(core.KindSMP, core.ShapeLink); got != "link" {
                t.Errorf("SMP link target word = %q, want link", got)
        }
        if got := banTargetWord(core.KindSMP, core.ShapeBareID); got != "user" {
                t.Errorf("SMP user target word = %q, want user", got)
        }
}

// TestBanRestrictionSummaryXFTPDelete (v1.6.18): the XFTP delete line
// explains what the delete does versus the block (a plain no-file
// error, without the reason).
func TestBanRestrictionSummaryXFTPDelete(t *testing.T) {
        s := banRestrictionSummary(banChoices{del: true}, core.KindXFTP, core.ShapeBareID)
        if !strings.Contains(s, `no file" error`) || !strings.Contains(s, "without your reason") {
                t.Fatalf("XFTP delete-only summary: %q", s)
        }
        if strings.Contains(s, "queue") {
                t.Fatalf("XFTP delete summary must not talk about queues: %q", s)
        }
        s = banRestrictionSummary(banChoices{del: true}, core.KindSMP, core.ShapeBareID)
        if !strings.Contains(s, "queue and its undelivered messages") {
                t.Fatalf("SMP delete summary: %q", s)
        }
}

// TestReasonVisibleV1621: the reason select belongs to the option whose
// command records it FOR SOMEBODY TO SEE - the XFTP file block and the
// SMP LINK block (new joins see the BLOCKED error). The SMP user ban
// has no reason row at all ("Block link creation" has the expiry, not
// the reason).
func TestReasonVisibleV1621(t *testing.T) {
        // SMP user ban: no reason row, whatever is ticked
        for _, c := range []banChoices{
                {},
                {block: true},
                {linkCreation: true, noticeTTL: 172800},
                {block: true, linkCreation: true, del: true},
                {del: true},
        } {
                if reasonVisible(core.KindSMP, core.ShapeBareID, c) {
                        t.Fatalf("SMP user ban must never show the reason row: %+v", c)
                }
        }
        // SMP link ban: the reason rides the link block
        if reasonVisible(core.KindSMP, core.ShapeLink, banChoices{}) {
                t.Fatal("SMP link ban: the reason must be hidden when the link block is unticked")
        }
        if reasonVisible(core.KindSMP, core.ShapeLink, banChoices{block: true, linkCreation: true}) {
                t.Fatal("SMP link ban: stray user-ban flags must not show the reason")
        }
        if !reasonVisible(core.KindSMP, core.ShapeLink, banChoices{link: true}) {
                t.Fatal("SMP link ban: ticking the link block must show the reason")
        }
        if !reasonVisible(core.KindSMP, core.ShapeLink, banChoices{link: true, del: true}) {
                t.Fatal("SMP link ban: link block + delete must show the reason")
        }
        // XFTP: the reason row belongs to the file block (the shape is
        // irrelevant there - files carry no links)
        if reasonVisible(core.KindXFTP, core.ShapeLink, banChoices{}) {
                t.Fatal("XFTP: the reason must be hidden when the file block is unticked")
        }
        if !reasonVisible(core.KindXFTP, core.ShapeBareID, banChoices{block: true}) {
                t.Fatal("XFTP: ticking the file block must show the reason")
        }
        if reasonVisible(core.KindXFTP, core.ShapeLink, banChoices{del: true}) {
                t.Fatal("XFTP: delete alone must not show the reason")
        }
}

// TestBanReasonToSendV1621: the reason the server records follows the
// option that owns the reason select - the SMP link block / the XFTP
// file block; everywhere else the official default (spam) is sent, so
// a value chosen for an option that is not happening never reaches the
// server.
func TestBanReasonToSendV1621(t *testing.T) {
        cases := []struct {
                k     core.Kind
                shape core.TargetShape
                ch    banChoices
                want  string
        }{
                // SMP link ban: the reason select lives on the link block
                {core.KindSMP, core.ShapeLink, banChoices{link: true, reason: core.BanReasonContent}, core.BanReasonContent},
                {core.KindSMP, core.ShapeLink, banChoices{link: true, del: true, reason: core.BanReasonContent}, core.BanReasonContent},
                {core.KindSMP, core.ShapeLink, banChoices{reason: core.BanReasonContent}, core.BanReasonSpam},
                // SMP user ban: no reason row - always the server default
                {core.KindSMP, core.ShapeBareID, banChoices{block: true, reason: core.BanReasonContent}, core.BanReasonSpam},
                {core.KindSMP, core.ShapeBareID, banChoices{block: true, linkCreation: true, reason: core.BanReasonContent}, core.BanReasonSpam},
                {core.KindSMP, core.ShapeBareID, banChoices{linkCreation: true, reason: core.BanReasonContent}, core.BanReasonSpam},
                // XFTP: the reason select lives on the file block
                {core.KindXFTP, core.ShapeBareID, banChoices{block: true, reason: core.BanReasonContent}, core.BanReasonContent},
                {core.KindXFTP, core.ShapeBareID, banChoices{block: true, linkCreation: true, reason: core.BanReasonContent}, core.BanReasonContent},
                {core.KindXFTP, core.ShapeBareID, banChoices{linkCreation: true, reason: core.BanReasonContent}, core.BanReasonSpam},
                {core.KindXFTP, core.ShapeBareID, banChoices{reason: core.BanReasonContent}, core.BanReasonSpam},
        }
        for _, c := range cases {
                if got := banReasonToSend(c.k, c.shape, c.ch); got != c.want {
                        t.Errorf("banReasonToSend(%v, %v, %+v) = %q, want %q", c.k, c.shape, c.ch, got, c.want)
                }
        }
}

// TestBanDialogLabelsV1621 (updated v1.6.27): the source carries the
// three per-target option sets - the user ban's "Block the queue" and
// "Block link creation" (with its expiry select), the link ban's
// "Block the link" (with the reason row), the file dialog's official
// methods; the old all-in-one labels and the by-hash labels are gone.
// v1.6.28: the two v1.6.27 meta header labels are gone too - their
// single 150-character lines made the dialog wider than laptop-size
// windows, which stopped Fyne from centering it.
func TestBanDialogLabelsV1621(t *testing.T) {
        src, err := os.ReadFile("admin.go")
        if err != nil {
                t.Skip("source not readable from test dir")
        }
        s := string(src)
        for _, want := range []string{
                "\"Block the queue\"",     // user-ban option 1 (v1.6.27)
                "\"Block link creation\"", // user-ban option 2
                "For how long:",           // the expiry select on option 2 (indented label)
                "\"Block the link\"",      // the link dialog's block option
                "\"Delete the queue permanently\"", // v1.6.33: the link dialog's delete option
                "linkBanDialog",           // ... and its own entry point
                "\"Ban this link?\"",      // the link dialog's question
                "Deleted the queue behind the link permanently", // v1.6.33: runBan's link-delete line
                "\"Block the file (official content moderation)\"", // XFTP option 1
                "\"Delete the file instead (no reason shown)\"",    // XFTP option 2 (delete)
                "capToWindow(v.c.win, body)",                       // v1.6.28: the dialog body is capped to the window
        } {
                if !strings.Contains(s, want) {
                        t.Errorf("admin.go must contain the label %s", want)
                }
        }
        // the retired v1.6.21-in-progress wording (the notice option doing
        // double duty as the link block) is gone, and so is the upload-
        // password option (added then removed in v1.6.21 - the file
        // server has no uploader identity to restrict, and create_password
        // is the operator's settings lever, not a ban action)
        for _, gone := range []string{
                "If this queue carries a live invitation, contact or group address",
                "tick it first",
                "Send the ban notice to their app",
                "Block file uploads",
                "Refuses further uploads",
                "Restrict uploads server-wide",
                "Client ID, address or link:",
                "Strip sending and receiving",
                "Block the connection to that link",
                // v1.6.28: the meta header labels and the verbose
                // block-link-creation explanation are gone
                "Pasted target is",
                "The options below are",
                "official SimpleX servers stop spammers",
                // v1.6.27: the by-hash dialog, the digest column and the
                // hash-accepting placeholders are removed with the feature
                "banDialogByDigest",
                "runBanByDigest",
                "NormalizeFileDigest",
                "FilesByDigest",
                "FileDigestHex",
                "digestShort",
                "rowFileDigest",
                "Content SHA-256",
                "cell(\"SHA-256\"",
                "File ID or content hash",
                "its SHA-256 content hash",
                // v1.6.32 made the link dialog ONE option and retired the
                // delete wording; v1.6.33 brought the delete back as a
                // real option (runBan's line is pinned in the wanted list
                // above) - only the old v1.6.21 phrasing stays gone
                "Delete the queue behind this link permanently",
        } {
                if strings.Contains(s, gone) {
                        t.Errorf("admin.go must no longer contain %q", gone)
                }
        }
        // the expiry select exists only for the user ban's option 2
        if strings.Count(s, "noticeExpiryOptions") < 2 {
                t.Error("the notice expiry options must be defined and used")
        }
}

// TestBanDialogStaysCentered (v1.6.28): the ban dialog's min size must
// fit the window again - the slimmed copy plus capToWindow keep it a
// centered box. Fyne clamps a popup whose min size exceeds the canvas
// to the full window at (0,0), which is how every dialog came to hug
// the top-left corner on laptop-size windows (the v1.6.27 dialog's
// natural size was about 985x806). 900x600 reproduces the old failure;
// the dialog must sit strictly inside the canvas with positive
// coordinates.
func TestBanDialogStaysCentered(t *testing.T) {
        a := test.NewApp()
        defer a.Quit()
        w := a.NewWindow("")
        w.Resize(fyne.NewSize(900, 600))
        c := &console{app: a, win: w, glog: &guiLog{}}
        v := &adminView{c: c, selected: core.KindSMP}
        v.banIDEntry = widget.NewEntry()
        v.lastQueues = nil

        const queueID = "LcJUMfVhwD8yxjAiSaDzzGF3-kLG4Uh0Fl_ZIjrRwjI"
        v.banIDEntry.SetText(queueID)
        v.banDialog(queueID)
        checkCentered(t, w, "user ban dialog")

        // the LINK variant - a pasted https short link derives its ID
        const linkKey = "lrdvu2d8A1GumSmoKb2krQmtKhWXq-tyGpHuM7aMwsw"
        link := "https://smp6.simplex.im/g#" + linkKey
        v.banIDEntry.SetText(link)
        v.banDialog(core.ParseTargetID(link))
        checkCentered(t, w, "link ban dialog")
}

// checkCentered asserts that the topmost overlay popup sits strictly
// inside the canvas and is not clamped to a corner (the overlay
// container holds the popup as its Content).
func checkCentered(t *testing.T, w fyne.Window, what string) {
        t.Helper()
        ols := w.Canvas().Overlays().List()
        if len(ols) == 0 {
                t.Fatalf("%s: no overlay was shown", what)
        }
        ov := reflect.ValueOf(ols[len(ols)-1])
        if ov.Kind() == reflect.Ptr {
                ov = ov.Elem()
        }
        f := ov.FieldByName("Content")
        if !f.IsValid() || f.IsZero() {
                t.Fatalf("%s: the overlay carries no Content popup", what)
        }
        popup, ok := f.Interface().(fyne.CanvasObject)
        if !ok {
                t.Fatalf("%s: the overlay Content is not a canvas object", what)
        }
        pos, size := popup.Position(), popup.Size()
        cs := w.Canvas().Size()
        t.Logf("%s: popup pos %v size %v (canvas %v, min %v)", what, pos, size, cs, popup.MinSize())
        if pos.X <= 0 || pos.Y <= 0 {
                t.Fatalf("%s: popup pinned to the top-left corner: pos %v size %v (min %v)",
                        what, pos, size, popup.MinSize())
        }
        if pos.X+size.Width > cs.Width+0.5 || pos.Y+size.Height > cs.Height+0.5 {
                t.Fatalf("%s: popup overflows the canvas: pos %v size %v (canvas %v)", what, pos, size, cs)
        }
}

// TestQueueWhenPartsPrecision (v1.6.27): the XFTP time column prefers
// the stored chunk file's own mtime and renders it with SECONDS
// (HH:MM:SS); rows whose chunks are gone fall back to the journal's
// hour-rounded creation time (HH:MM) - and the day-grouping sort key
// follows the same preference.
func TestQueueWhenPartsPrecision(t *testing.T) {
        // mtime known: second precision
        q := core.QueueInfo{ID: "R1", FileID: "S1", UploadedAt: "1761001672", CreatedAt: "1761000000"}
        d, hm := queueWhenParts(core.KindXFTP, q)
        if hm == "" || len(hm) != 8 || hm[2] != ':' || hm[5] != ':' {
                t.Fatalf("mtime row must render HH:MM:SS, got %q (date %q)", hm, d)
        }
        if got := queueWhenEpoch(core.KindXFTP, q); got != 1761001672 {
                t.Errorf("queueWhenEpoch with mtime = %d, want 1761001672", got)
        }
        // no stored file: the hour-precision journal time, HH:MM
        q = core.QueueInfo{ID: "R1", FileID: "S1", CreatedAt: "1761000000"}
        d, hm = queueWhenParts(core.KindXFTP, q)
        if len(hm) != 5 || hm[2] != ':' {
                t.Fatalf("journal-hour row must render HH:MM, got %q (date %q)", hm, d)
        }
        // (no ":00" suffix assertion: the journal rounds to the UTC hour,
        // but sxops renders local time - non-whole-hour zone offsets shift
        // the minutes)
        if got := queueWhenEpoch(core.KindXFTP, q); got != 1761000000 {
                t.Errorf("queueWhenEpoch fallback = %d, want 1761000000", got)
        }
        // SMP rows are unaffected (first-seen time)
        d, hm = queueWhenParts(core.KindSMP, core.QueueInfo{ID: "Q1"})
        if d != "" || hm != "" {
                t.Errorf("SMP row without SeenAt must render nothing, got %q / %q", d, hm)
        }
}
