package stdlib

import (
	"slices"
	"testing"

	"github.com/connerohnesorge/gohard/internal/token"
)

// TestNVC2008Set: the 2008 view of ieee and std has exactly NVC's
// VHDL-2008 declaration set (#33 Done when), up to the reviewed
// differences in nvcLacks and nvcExtra (README.md).
func TestNVC2008Set(t *testing.T) {
	ref := readSet(t, "nvc", token.Std2008)
	view := map[string]bool{}
	for _, d := range ViewOf(token.Std2008).Decls() {
		k := MatchKey(d)
		view[k] = true
		if !ref[k] && !nvcLacks[k] {
			t.Errorf("view 2008 has %q; NVC 2008 does not", k)
		}
	}
	for _, k := range slices.Sorted(mapKeys(ref)) {
		if !view[k] && !nvcExtra[k] {
			t.Errorf("NVC 2008 has %q; view 2008 does not", k)
		}
	}
}

// nvcLacks are declarations of the 2008 view NVC's 2008 text does not
// make, and nvcExtra the reverse; each is reviewed in README.md.
// NVC's textio declares the attribute NEVER_WAITS, an NVC-private
// optimization hint on its read procedures that the LRM text does not
// have.
var (
	nvcLacks = map[string]bool{}
	nvcExtra = map[string]bool{"std.textio NEVER_WAITS": true}
)
