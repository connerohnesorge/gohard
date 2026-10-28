package simrun

import (
	"strings"
	"testing"

	"github.com/connerohnesorge/gohard/v2/internal/diag"
	"github.com/connerohnesorge/gohard/v2/internal/elab"
	"github.com/connerohnesorge/gohard/v2/internal/sim"
)

// TestMissingCodeHasPosition (#109): a process the interpreter gives no
// code is an unsupported/construct at the process's source position,
// not a positionless one.
func TestMissingCodeHasPosition(t *testing.T) {
	o := raceOpts(t, false)
	next := o.Code
	o.Code = func(d *elab.Design, p *elab.Proc) (sim.Code, error) {
		if strings.HasSuffix(p.Path, ".w") {
			return nil, nil
		}
		return next(d, p)
	}
	out := Run(o)
	if len(out.Diagnostics) != 1 {
		t.Fatalf("diagnostics: %v", out.Diagnostics)
	}
	d := out.Diagnostics[0]
	if d.Code != diag.UnsupportedConstruct || d.Path != "tb.vhd" ||
		d.Line != 8 || d.Col != 3 ||
		!strings.Contains(d.Message, "tb.w (no interpreter code)") {
		t.Fatalf("got %s:%d:%d: %s %s", d.Path, d.Line, d.Col, d.Code,
			d.Message)
	}
}
