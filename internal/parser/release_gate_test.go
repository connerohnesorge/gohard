package parser

import (
	"slices"
	"testing"

	"github.com/connerohnesorge/gohard/v2/internal/token"
)

// TestReleaseBeforeVHDL2008 pins #7's keyword rule for release: before
// 2008 the word lexes as an identifier and syntax/standard fires only
// where the parse needs the keyword. `s <= release;` needs no keyword
// there: it is an ordinary assignment of a signal named release
// (no diagnostic from the parser; an undeclared name is sema's
// sema/undefined). `s <= release out;` does need it: syntax/standard,
// recovered as a release assignment. From 2008 both are release
// assignments with no diagnostic. The tree shape of each text is the
// same under every standard except for the plain assignment, whose
// meaning the standard changes.
func TestReleaseBeforeVHDL2008(t *testing.T) {
	wrap := func(stmt string) []byte {
		return []byte("architecture a of e is begin process begin " +
			stmt + " wait; end process; end;")
	}
	for _, std := range token.Standards {
		pre := std < token.Std2008
		plain := checkInvariants(t, "plain", wrap("s <= release;"),
			Options{Standard: std})
		if got := problemCodes(plain); len(got) != 0 {
			t.Errorf("%v: s <= release; problems %q", std, got)
		}
		isRelease := slices.Contains(kindsPath(plain),
			"ReleaseAssignStmt")
		if isRelease == pre {
			t.Errorf("%v: s <= release; parsed as release assignment "+
				"= %v, want %v", std, isRelease, !pre)
		}
		out := checkInvariants(t, "out", wrap("s <= release out;"),
			Options{Standard: std})
		rule, _ := RuleByID("release")
		if fired := ruleFired(out, rule) > 0; fired != pre {
			t.Errorf("%v: s <= release out; syntax/standard = %v, "+
				"want %v; problems %q", std, fired, pre,
				problemCodes(out))
		}
		if !slices.Contains(kindsPath(out), "ReleaseAssignStmt") {
			t.Errorf("%v: s <= release out; is not a release "+
				"assignment", std)
		}
	}
}
