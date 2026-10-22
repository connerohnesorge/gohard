package elab

// elab_natives.go answers the std.env calls made during elaboration
// (#18): a generic's actual or default, a signal's initial value or a
// design constant that calls TOOL_NAME, VHDL_VERSION, GETENV,
// FILE_LINE, the clock, ... is evaluated by the static evaluator,
// which hands these calls to Design.natives (sema.Natives). The
// answers are those the interpreter gives (interp_env.go) at
// simulated time 0, from the same EnvOptions. What depends on a
// running process (GET_CALL_PATH, the assert API) or allocates (the
// LINE forms) stays not static.

import (
	"strconv"
	"strings"

	"github.com/connerohnesorge/gohard/internal/cst"
	"github.com/connerohnesorge/gohard/internal/eval"
	"github.com/connerohnesorge/gohard/internal/sema"
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
		if unitOf(x) != "std.env" || rt == nil ||
			rt.Kind == eval.KindAccess {
			return eval.Value{}, false, nil
		}
		as := make([]arg, len(args))
		for i, v := range args {
			as[i] = arg{v: v}
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
			"-":
		default:
			return eval.Value{}, false, nil
		}
		v, err := m.envFunc(x, as, rt)
		return v, true, err
	}
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
