package core

import (
	"strings"
	"testing"
)

// v1.6.28 dropped the compact simplex:/ scheme on the belief that it is
// unused (not a web URL, only the https forms get shared). v1.6.31
// REVERTED that: the operator confirmed the apps do share the compact
// forms, and the upstream parser accepts them exactly like the https
// ones (Agent/Protocol.hs AConnShortLink authorityP / ServiceScheme -
// "simplex:" is the SSSimplex scheme; connReqUriP for the full links).
// The one upstream difference: a compact SHORT link has no authority, so
// its server must ride in the ?h= query param - without it the parser
// fails ("short link without server"), and sxops rejects it too.
// The https:// scan being case-insensitive (the other v1.6.28 fix)
// stays, and now covers the compact scheme as well.

// TestValidateComplaintsSimplexCompactReversed: the compact spellings of
// the durable contact forms are accepted again - a well-formed compact
// short address saves, and the compact full link parses - while the
// compact short form without its required ?h= server part is still
// rejected, with the error naming what is missing.
func TestValidateComplaintsSimplexCompactReversed(t *testing.T) {
	key := "lrdvu2d8A1GumSmoKb2krQmtKhWXq-tyGpHuM7aMwsw"
	esc := "smp%3A%2F%2FPQUV2eL0t7OStZOoAsPEV2QYWt4-xilbakvGUGOItUo%3D%40smp6.simplex.im%2FK1rslx-m5bpXVIdMZg9NLUZ_8JBm8xTt%23MCowBQYDK2VuAyEALDeVe-sG8mRY22LsXlPgiwTNs9dbiLrNuA7f3ZMAJ2w%3D"

	if err := ValidateComplaintsSimplex("simplex:/a#" + key + "?h=smp6.simplex.im"); err != nil {
		t.Errorf("the compact short contact form must be accepted again: %v", err)
	}
	if err := ValidateComplaintsSimplex("simplex:/contact#/?v=1&smp=" + esc); err != nil {
		t.Errorf("the compact full contact link must be accepted again: %v", err)
	}

	// without the ?h= server part the upstream parser fails - so does
	// sxops, and the error says what is missing
	err := ValidateComplaintsSimplex("simplex:/a#" + key)
	if err == nil {
		t.Fatal("a compact short form without ?h= must be rejected (upstream: \"short link without server\")")
	}
	if !strings.Contains(err.Error(), "?h=") {
		t.Errorf("the missing-h rejection should name the ?h= server part, got: %v", err)
	}
}

// TestParseTargetIDShapeCaseInsensitiveScheme: links copied out of
// notes or PDFs can carry an upper-case scheme - they parse to the same
// target and shape as their lower-case originals (v1.6.28 introduced
// this for HTTPS://; v1.6.31 covers the compact scheme too - see
// v1631_test.TestParseTargetIDCompactForms).
func TestParseTargetIDShapeCaseInsensitiveScheme(t *testing.T) {
	const queueID = "K1rslx-m5bpXVIdMZg9NLUZ_8JBm8xTt"
	escURI := "smp%3A%2F%2FPQUV2eL0t7OStZOoAsPEV2QYWt4-xilbakvGUGOItUo%3D%40smp6.simplex.im%2F" + queueID + "%23MCowBQYDK2VuAyEALDeVe-sG8mRY22LsXlPgiwTNs9dbiLrNuA7f3ZMAJ2w%3D"
	id, shape := ParseTargetIDShape("HTTPS://SIMPLEX.IM/CONTACT#/?v=1&smp=" + escURI + "&data=abc")
	if shape != ShapeLink || id != queueID {
		t.Errorf("upper-case full link = (%q, %v), want (%q, ShapeLink)", id, shape, queueID)
	}
	// SHA-256("") as a 32-byte base64url link key - its derived link
	// ID is pinned by v1613_test.TestDeriveContactLinkID
	const key = "47DEQpj8HBSa-_TImW-5JCeuQeRkm5NMpJWZG3hSuFU"
	if got := ParseTargetID("HTTPS://SMP.EXAMPLE.COM/A#" + key); got != "fiiJAPUyVfD7G5GmZYavocyACx6M4PGC" {
		t.Errorf("upper-case short link ID = %q, want fiiJAPUyVfD7G5GmZYavocyACx6M4PGC", got)
	}
	// the compact scheme in upper case parses too
	if got := ParseTargetID("SIMPLEX:/G#" + key + "?h=smp.example.com"); got != "fiiJAPUyVfD7G5GmZYavocyACx6M4PGC" {
		t.Errorf("upper-case compact link ID = %q, want fiiJAPUyVfD7G5GmZYavocyACx6M4PGC", got)
	}
}
