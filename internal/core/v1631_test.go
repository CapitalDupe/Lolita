package core

import (
	"encoding/base64"
	"encoding/hex"
	"strings"
	"testing"
)

// v1.6.31 - three moderation fixes, each verified against the simplexmq
// stable source:
//
//   1. THE LIST NO LONGER TREATS A USER AS A LINK. Only contact-mode
//      queues are links: every reusable link kind (contact address /
//      channel / group / relay) is created via QRContact -> queue_mode=C
//      carrying its derived link ID (Agent.hs prepareLinkData), and LSET
//      - the source of the LINK journal record - is sent only for
//      contact connections (setConnShortLinkAsync). A queue_mode=M
//      queue's link_id is the leftover of a ONE-TIME invitation: the
//      server generated the ID at random, the first join consumed the
//      link, and no LDELETE follows - that queue is an ordinary user's
//      chat queue (v1.6.29-30 marked it "link", so every one-time
//      invitation connection was mislabelled - the operator's own
//      report, and the reason the ban dialog opened the wrong variant
//      for users).
//
//   2. THE COMPACT simplex:/ LINK FORMS ARE BAN INPUTS AGAIN. v1.6.28
//      dropped them on the belief they are unused; the operator
//      confirmed the apps do share them, and they parse upstream exactly
//      like the https forms (AConnShortLink strP / connReqUriP - the
//      scheme is the only difference; the compact short form carries
//      the server in its ?h= query).
//
//   3. THE COMPLAINTS ADDRESS ACCEPTS THE COMPACT FORMS TOO - the same
//      upstream grammar decides what the server can parse in
//      complaints_simplex, so rejecting the compact spelling there only
//      rejected the operator's own valid address.

// TestParseStoreLogContactModeLinks: the link markers follow the queue
// mode. A queue_mode=C CREATE with link_id is a live link; a queue_mode=M
// CREATE with the very same link_id shape is NOT (one-time invitation
// leftover - an ordinary user's chat queue); a LINK record stays a live
// link whatever the CREATE said (LSET is contact-only upstream, so it is
// a genuine address link); LDELETE still consumes it.
func TestParseStoreLogContactModeLinks(t *testing.T) {
	log := "CREATE rid=r1 rk=k1 rdh=d1 sid=q1 sk=s2 queue_mode=M link_id=lnk1AAAAAAAA queue_data=d1\n" + // one-time invitation leftover: a USER
		"CREATE rid=r2 rk=k1 rdh=d1 sid=q2 sk=s2 queue_mode=C link_id=lnk2AAAAAAAA queue_data=d2\n" + // contact address: a LINK
		"CREATE rid=r3 rk=k1 rdh=d1 sid=q3 sk=s2 link_id=lnk3AAAAAAAA queue_data=d3\n" + // no mode token (old journal): no link
		"CREATE rid=r4 rk=k1 rdh=d1 sid=q4 sk=s2 queue_mode=M\n" + // plain messaging queue
		"CREATE rid=r5 rk=k1 rdh=d1 sid=q5 sk=s2 queue_mode=M\n" +
		"LINK r5 lnk5AAAAAAAA datablob\n" + // LSET: a genuine contact link
		"CREATE rid=r6 rk=k1 rdh=d1 sid=q6 sk=s2 queue_mode=C link_id=lnk6AAAAAAAA queue_data=d6\n" +
		"LDELETE r6\n" + // consumed: not live
		"CREATE rid=r7 rk=k1 rdh=d1 sid=q7 sk=s2 queue_mode=C link_id=lnk7AAAAAAAA queue_data=d7\n" +
		"CREATE rid=r7 rk=k1 rdh=d1 sid=q7b sk=s2 queue_mode=C\n" // re-created without a link: clears
	rows := ParseStoreLog(KindSMP, []byte(log))
	byID := map[string]QueueInfo{}
	for _, r := range rows {
		byID[r.ID] = r
	}
	if len(rows) != 7 {
		t.Fatalf("rows = %d, want 7: %+v", len(rows), rows)
	}
	if q1 := byID["q1"]; q1.LinkID != "" || q1.LinkLive {
		t.Errorf("q1 (mode M with link_id - one-time invitation leftover) must not be a link: %+v", q1)
	}
	if q2 := byID["q2"]; q2.LinkID != "lnk2AAAAAAAA" || !q2.LinkLive {
		t.Errorf("q2 (mode C with link_id - contact address) must be a live link: %+v", q2)
	}
	if q3 := byID["q3"]; q3.LinkID != "" || q3.LinkLive {
		t.Errorf("q3 (link_id without a queue_mode token - old journal) must not be a link: %+v", q3)
	}
	if q4 := byID["q4"]; q4.LinkID != "" || q4.LinkLive {
		t.Errorf("q4 (plain messaging queue) must not be a link: %+v", q4)
	}
	if q5 := byID["q5"]; q5.LinkID != "lnk5AAAAAAAA" || !q5.LinkLive {
		t.Errorf("q5 (LINK record - the LSET contact link) must be a live link: %+v", q5)
	}
	if q6 := byID["q6"]; q6.LinkID != "lnk6AAAAAAAA" || q6.LinkLive {
		t.Errorf("q6's link was consumed (LDELETE): %+v", q6)
	}
	if q7 := byID["q7b"]; q7.LinkID != "" || q7.LinkLive {
		t.Errorf("re-created queue must have its earlier link cleared: %+v", q7)
	}
}

// TestParseTargetIDCompactForms: the compact simplex:/ spellings parse
// to the same targets and shapes as their https counterparts - the
// short forms (reusable kinds derive the link ID from the key; the
// one-time kind carries it), the full contact / invitation forms, and
// links embedded in report prose.
func TestParseTargetIDCompactForms(t *testing.T) {
	key, _ := hex.DecodeString("e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855")
	tok := base64.RawURLEncoding.EncodeToString(key)
	id1 := "fiiJAPUyVfD7G5GmZYavocyACx6M4PGC"
	const oneTimeID = "LNKCOMPACTTEST01AbCdEfGh"
	const queueID = "K1rslx-m5bpXVIdMZg9NLUZ_8JBm8xTt"
	const keyHash = "PQUV2eL0t7OStZOoAsPEV2QYWt4-xilbakvGUGOItUo="
	escURI := "smp%3A%2F%2FPQUV2eL0t7OStZOoAsPEV2QYWt4-xilbakvGUGOItUo%3D%40smp6.simplex.im%2F" + queueID + "%23MCowBQYDK2VuAyEALDeVe-sG8mRY22LsXlPgiwTNs9dbiLrNuA7f3ZMAJ2w%3D"

	cases := []struct {
		name      string
		in        string
		wantID    string
		wantShape TargetShape
	}{
		{"compact contact address", "simplex:/a#" + tok + "?h=smp.example.com", id1, ShapeLink},
		{"compact group link", "simplex:/g#" + tok + "?h=smp.example.com", id1, ShapeLink},
		{"compact channel link", "simplex:/c#" + tok + "?h=smp.example.com&p=5223", id1, ShapeLink},
		{"compact relay link", "simplex:/r#" + tok, id1, ShapeLink}, // ?h= optional for the ban form (the derived ID is the same)
		{"compact one-time link", "simplex:/i#" + oneTimeID + "/" + tok + "?h=smp.example.com", oneTimeID, ShapeLink},
		{"compact full contact link", "simplex:/contact#/?v=1&smp=" + escURI + "&data=abc", queueID, ShapeLink},
		{"compact full invitation link", "simplex:/invitation#/?v=1&smp=" + escURI + "&e2e=abc", queueID, ShapeLink},
		{"compact link in report text", "abuse report: simplex:/g#" + tok + "?h=smp.example.com spamming", id1, ShapeLink},
		{"upper-case compact scheme", "SIMPLEX:/G#" + tok + "?h=smp.example.com", id1, ShapeLink},
		// the https forms keep parsing (v1.6.22-30 behaviour)
		{"https short link still parses", "https://smp.example.com/g#" + tok, id1, ShapeLink},
		{"https full link still parses", "https://simplex.im/contact#/?v=1&smp=" + escURI, queueID, ShapeLink},
		// a compact link nested in a URL is not mistaken for one
		{"compact form nested in a URL", "https://example.com/page/simplex:/g#" + tok, "", ShapeBareID},
	}
	for _, c := range cases {
		gotID, gotShape := ParseTargetIDShape(c.in)
		if gotID != c.wantID || gotShape != c.wantShape {
			t.Errorf("%s: ParseTargetIDShape = (%q, %v), want (%q, %v)", c.name, gotID, gotShape, c.wantID, c.wantShape)
		}
	}
}

// TestValidateComplaintsCompactForms: the complaints address accepts the
// compact contact spellings exactly like the https ones, requires the
// ?h= server param on the compact short form (the upstream parser
// refuses it otherwise - "short link without server"), and keeps
// rejecting the one-time invitation spellings in both schemes.
func TestValidateComplaintsCompactForms(t *testing.T) {
	key := "lrdvu2d8A1GumSmoKb2krQmtKhWXq-tyGpHuM7aMwsw"
	esc := "smp%3A%2F%2FPQUV2eL0t7OStZOoAsPEV2QYWt4-xilbakvGUGOItUo%3D%40smp6.simplex.im%2FK1rslx-m5bpXVIdMZg9NLUZ_8JBm8xTt%23MCowBQYDK2VuAyEALDeVe-sG8mRY22LsXlPgiwTNs9dbiLrNuA7f3ZMAJ2w%3D"

	accepted := map[string]string{
		"compact full contact link": "simplex:/contact#/?v=1&smp=" + esc,
		"compact short contact":     "simplex:/a#" + key + "?h=smp6.simplex.im",
		"compact short with port":   "simplex:/a#" + key + "?h=smp6.simplex.im&p=5223&c=c29tZWhhc2g",
		"https short contact":       "https://smp6.simplex.im/a#" + key,
	}
	for name, s := range accepted {
		if err := ValidateComplaintsSimplex(s); err != nil {
			t.Errorf("%s must be accepted: %v", name, err)
		}
	}

	rejected := map[string]string{
		"compact short without h=":
			// upstream serverQueryP: noServer for the simplex: scheme
			// without ?h= ("short link without server")
			"simplex:/a#" + key,
		"compact one-time invitation": "simplex:/i#" + key + "/" + key + "?h=smp6.simplex.im",
		"compact full invitation":     "simplex:/invitation#/?v=1&smp=" + esc,
		"https one-time invitation":   "https://smp6.simplex.im/i#" + key + "/" + key,
	}
	for name, s := range rejected {
		err := ValidateComplaintsSimplex(s)
		if err == nil {
			t.Errorf("%s must be rejected (the server would not start): %q", name, s)
			continue
		}
		if name == "compact short without h=" && !strings.Contains(err.Error(), "?h=") {
			t.Errorf("the missing-h rejection should say what is missing: %v", err)
		}
	}

	// the saved-form description recognises the compact spelling too
	if got := ComplaintsSimplexForm("simplex:/a#" + key + "?h=smp6.simplex.im"); got != "contact address (short link)" {
		t.Errorf("compact short form description = %q, want \"contact address (short link)\"", got)
	}
	if got := ComplaintsSimplexForm("simplex:/contact#/?v=1&smp=" + esc); got != "full contact link" {
		t.Errorf("compact full form description = %q, want \"full contact link\"", got)
	}
}
