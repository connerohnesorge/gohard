package resolve

import (
	"strings"
	"testing"

	"github.com/connerohnesorge/gohard/internal/config"
	"github.com/connerohnesorge/gohard/internal/sema"
)

const ambiguousSrc = `package p1 is
  function scale(x : integer) return integer;
end;
package p2 is
  function scale(x : integer) return integer;
  function scale(x : boolean) return integer;
end;
use work.p1.all;
use work.p2.all;
entity e is end;
architecture rtl of e is
  constant k : integer := scale(3);
  constant b : integer := scale(true);
begin
end;
`

// TestOverloadOneResult: the ambiguous-overload diagnostic, hover on
// the call and the overload result completion reads inside its
// argument list (sema.Result.OverloadAt) are one editor-mode result
// (#34): the same candidates, in the same order, with the same
// reasons; and hover on the resolved call shows the chosen overload
// and its package.
func TestOverloadOneResult(t *testing.T) {
	r := sema.Analyze(sema.Input{Files: []sema.Source{{Path: "a.vhd",
		Text: []byte(ambiguousSrc)}}, Config: config.Defaults()})
	if len(r.Diagnostics) != 1 ||
		r.Diagnostics[0].Code != "sema/ambiguous-overload" {
		t.Fatalf("diagnostics: %v", r.Diagnostics)
	}
	msg := r.Diagnostics[0].Message
	call := strings.Index(ambiguousSrc, "scale(3)")
	h, ok := HoverAt(r, "a.vhd", call)
	if !ok || h.Overload == nil || h.Overload.Chosen != nil {
		t.Fatalf("hover: %+v", h)
	}
	o, span, ok := r.OverloadAt("a.vhd", call+len("scale("))
	if !ok || o != h.Overload ||
		ambiguousSrc[span.Start:span.End] != "scale(3)" {
		t.Fatalf("OverloadAt in the arguments: %v %v", o, span)
	}
	fits := 0
	for _, c := range o.Candidates {
		line := "- " + Describe(c.Decl) + ": "
		if c.Fits {
			fits++
			line += "fits"
			if !strings.Contains(msg, sema.Signature(c.Decl)+" from "+
				sema.Origin(c.Decl)) {
				t.Errorf("diagnostic lacks candidate %s: %s",
					Describe(c.Decl), msg)
			}
		} else {
			line += c.Reason
		}
		if !strings.Contains(h.Markdown, line) {
			t.Errorf("hover lacks %q:\n%s", line, h.Markdown)
		}
	}
	if fits != 2 || len(o.Candidates) != 3 {
		t.Errorf("%d of %d candidates fit, want 2 of 3", fits,
			len(o.Candidates))
	}
	// The resolved call: hover names the chosen overload's package.
	h, _ = HoverAt(r, "a.vhd", strings.Index(ambiguousSrc, "scale(true)"))
	if !strings.Contains(h.Markdown,
		"function scale [BOOLEAN return INTEGER] from work.p2") {
		t.Errorf("hover scale(true): %q", h.Markdown)
	}
	if _, _, ok := r.OverloadAt("a.vhd", strings.Index(ambiguousSrc,
		"entity")); ok {
		t.Error("OverloadAt outside any call")
	}
}
