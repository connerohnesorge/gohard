package elab

// interp_env.go holds the std.env natives (LRM 16.5): the tool and
// time queries, STOP and FINISH.

import (
	"math"

	"github.com/connerohnesorge/gohard/internal/eval"
	"github.com/connerohnesorge/gohard/internal/sema"
)

// envFunc evaluates a std.env function.
func (m *machine) envFunc(x *sema.Decl, args []arg, rt *eval.Type,
) (eval.Value, error) {
	switch x.Name {
	case "resolution_limit":
		return eval.Phys(rt, 1), nil
	case "tool_type":
		return strValue(rt, "SIMULATION"), nil
	case "tool_vendor":
		return strValue(rt, "GoHard"), nil
	case "tool_name":
		return strValue(rt, "gohard"), nil
	case "tool_edition", "tool_version":
		return strValue(rt, ""), nil
	case "vhdl_version":
		return strValue(rt, "2008"), nil
	case "time_to_seconds":
		return eval.RealValue(rt, float64(args[0].v.I)*1e-15), nil
	case "seconds_to_time":
		return eval.Phys(rt, int64(math.Round(args[0].v.F*1e15))), nil
	}
	return eval.Value{}, unsupported(x)
}

// envProc executes a std.env procedure; stop reports STOP or FINISH,
// after which the process does not go on.
func (m *machine) envProc(n *node, x *sema.Decl, args []arg,
) (stop bool, err error) {
	switch x.Name {
	case "stop", "finish":
		var status int64
		if len(args) > 0 {
			status = args[0].v.I
		}
		m.ctx.Stop(status, x.Name == "finish", n.src)
		return true, nil
	}
	return false, unsupported(x)
}
