package elab

// elab_natives.go answers the std.env calls made during elaboration
// (#18): a generic's actual or default, a signal's initial value or a
// design constant that calls TOOL_NAME, VHDL_VERSION, GETENV,
// FILE_LINE, the clock, ... is evaluated by the static evaluator,
// which hands these calls to Design.natives (sema.Natives). The
// answers are those the interpreter gives (interp_env.go) at
// simulated time 0, from the same EnvOptions: no assertion has run yet,
// so the assert API reports its initial state. NOW (0 fs), JUSTIFY and
// the implicit TO_STRING, TO_OSTRING and TO_HSTRING are answered the
// same way. A std.env function only a running process answers (the
// LINE forms, which allocate, GET_CALL_PATH, DIR_*, PslIsCovered) is
// the unsupported construct "std.env.X during elaboration" (please
// report), never a silent not-static value.

import (
	"strconv"
	"strings"

	"github.com/connerohnesorge/gohard/v2/internal/cst"
	"github.com/connerohnesorge/gohard/v2/internal/eval"
	"github.com/connerohnesorge/gohard/v2/internal/sema"
)

// natives returns the elaboration-time std.env of d under env.
func (d *Design) natives(env EnvOptions) sema.Natives {
	// m is a machine without a process or kernel: the clock reads
	// simulated time 0.
	m := &machine{w: newWorld(d, InterpOptions{Env: env}), idx: -1}
	files := map[*cst.Green]*sema.File{} // lookup only
	if d.r != nil {
		for _, f := range d.r.Files {
			if f.Parse != nil {
				files[f.Parse.Root] = f
			}
		}
	}
	return func(e *sema.Expr, x *sema.Decl, args []eval.Value,
		rt *eval.Type,
	) (eval.Value, bool, error) {
		if rt == nil {
			return eval.Value{}, false, nil
		}
		as := make([]arg, len(args))
		for i, v := range args {
			as[i] = arg{v: v}
		}
		if v, ok, err := m.elabBuiltin(x, as, rt); ok {
			return v, true, err
		}
		if unitOf(x) != "std.env" {
			return eval.Value{}, false, nil
		}
		if rt.Kind == eval.KindAccess {
			return eval.Value{}, true, elabUnsupported(x)
		}
		switch name := strings.Trim(string(x.Name), `"`); name {
		case "file_name", "file_path", "file_line":
			s, ok := nodeSite(files, e)
			if !ok {
				return eval.Value{}, false, nil
			}
			s = s.valid()
			switch {
			case name == "file_name":
				return strValue(rt, m.fileName(s)), true, nil
			case name == "file_path":
				return strValue(rt, m.fileDir(s)), true, nil
			case rt.Kind == eval.KindInteger:
				return eval.Int(rt, int64(s.line)), true, nil
			}
			return strValue(rt, strconv.Itoa(s.line)), true, nil
		case "to_string":
			if len(x.Params) == 0 ||
				typeKey(x.Params[0].Type) != "time_record" {
				return eval.Value{}, false, nil
			}
		case "resolution_limit", "tool_type", "tool_vendor",
			"tool_name", "tool_edition", "tool_version",
			"vhdl_version", "getenv", "time_to_seconds",
			"seconds_to_time", "epoch", "localtime", "gmtime", "+",
			"-", "isvhdlassertfailed", "getvhdlassertcount",
			"getvhdlassertenable", "getvhdlassertformat",
			"getvhdlreadseverity", "pslassertfailed",
			"getpslcoverassert":
			// The assert and PSL state before any process ran.
			// (PslIsCovered and PslIsAssertCovered count the
			// design's directives, which elaboration has not all
			// built yet.)
		default:
			return eval.Value{}, true, elabUnsupported(x)
		}
		v, err := m.envFunc(x, as, rt)
		return v, true, err
	}
}

// elabBuiltin answers the built-in functions outside std.env that a
// signal's initial value or a generic may call during elaboration:
// NOW (0 fs: elaboration precedes simulated time), JUSTIFY, and the
// implicit TO_STRING, TO_OSTRING and TO_HSTRING. ok false for any
// other function.
func (m *machine) elabBuiltin(x *sema.Decl, args []arg, rt *eval.Type,
) (eval.Value, bool, error) {
	fn := &callFn{x: x, ret: rt}
	switch u := unitOf(x); {
	case x.Implicit:
		switch x.Name {
		case "to_string", "to_ostring", "to_hstring":
			v, err := m.implicitFunc(fn, args)
			return v, true, err
		}
	case u == "std.standard" && x.Name == "now",
		u == "std.textio" && x.Name == "justify":
		v, err := m.builtinFunc(fn, 0, args)
		return v, true, err
	}
	return eval.Value{}, false, nil
}

// elabUnsupported is the error of a call of the std.env function x
// during elaboration that only a running process answers.
func elabUnsupported(x *sema.Decl) error {
	name := unitOf(x) + "." + x.Text
	return sema.NewUnsupported(name+" during elaboration",
		eval.NotStatic("call of "+x.Text))
}

// nodeSite is the place of the call e in the project files: its
// file's path and the line it starts on.
func nodeSite(files map[*cst.Green]*sema.File, e *sema.Expr) (site,
	bool,
) {
	if e == nil || e.Node.IsZero() {
		return site{}, false
	}
	root := e.Node
	for p := root.Parent(); !p.IsZero(); p = p.Parent() {
		root = p
	}
	f, ok := files[root.Green()]
	if !ok || f.Lines == nil {
		return site{}, false
	}
	l, _ := f.Lines.LineCol(e.Node.Span().Start)
	return site{path: f.Path, line: l}, true
}
