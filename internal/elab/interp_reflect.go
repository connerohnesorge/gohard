package elab

// interp_reflect.go is the home of the std.reflection natives
// (VHDL-2019, LRM 16.7). None exists yet: every call is a
// *nativeError. The natives are methods of the package's protected
// types, so they get the receiver object handle obj.

import (
	"github.com/connerohnesorge/gohard/internal/eval"
	"github.com/connerohnesorge/gohard/internal/sema"
)

// reflectFunc evaluates a std.reflection function (a method of obj).
func (m *machine) reflectFunc(x *sema.Decl, obj int64, args []arg,
	rt *eval.Type,
) (eval.Value, error) {
	_, _, _ = obj, args, rt
	return eval.Value{}, unsupported(x)
}

// reflectProc executes a std.reflection procedure (a method of obj).
func (m *machine) reflectProc(x *sema.Decl, obj int64, args []arg,
) error {
	_, _ = obj, args
	return unsupported(x)
}
