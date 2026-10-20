package elab

// interp_builtin.go holds the subprograms built into the kernel (#35
// R6): NOW, the implicit operations without an evaluator operation
// (TO_STRING, DEALLOCATE, the file operations, RISING_EDGE of BIT and
// BOOLEAN), std.env and std.textio. Files go through world (in memory)
// and InterpOptions.FS only.

import (
	"errors"
	"math"
	"strconv"
	"strings"

	"github.com/connerohnesorge/gohard/internal/cst"
	"github.com/connerohnesorge/gohard/internal/eval"
	"github.com/connerohnesorge/gohard/internal/sema"
	"github.com/connerohnesorge/gohard/internal/token"
)

// unitOf returns "lib.unit" of x's design unit.
func unitOf(x *sema.Decl) string {
	if x.Unit == nil {
		return ""
	}
	return string(x.Unit.Library) + "." + string(x.Unit.Name)
}

func unsupported(x *sema.Decl) error {
	return errors.New(unitOf(x) + "." + x.Text +
		" is not supported in simulation")
}

// builtinFunc evaluates a built-in function.
func (m *machine) builtinFunc(fn *callFn, args []arg) (eval.Value, error) {
	x := fn.x
	rt := fn.ret
	switch {
	case x.Name == "now" && unitOf(x) == "std.standard":
		if m.ctx == nil {
			return eval.Phys(rt, 0), nil
		}
		return eval.Phys(rt, int64(m.ctx.Now())), nil
	case unitOf(x) == "std.env":
		return m.envFunc(x, args, rt)
	case unitOf(x) == "std.textio" && x.Name == "justify":
		if len(args) < 3 {
			return eval.Value{}, unsupported(x)
		}
		return strValue(rt, justify(stringOf(args[0].v),
			args[1].v.I, args[2].v.I)), nil
	}
	if !x.Implicit {
		return eval.Value{}, unsupported(x)
	}
	switch x.Name {
	case "endfile":
		f, err := m.file(args[0].v)
		if err != nil {
			return eval.Value{}, err
		}
		if !f.open || f.mode != modeRead {
			return eval.Value{}, errors.New("endfile of a file not " +
				"open for reading")
		}
		return eval.Bool(f.pos >= len(f.data)), nil
	case "rising_edge", "falling_edge":
		p := args[0].sig
		if p == nil || m.ctx == nil {
			return eval.Bool(false), nil
		}
		ev, err := m.ctx.PartEvent(p.t)
		if err != nil || !ev {
			return eval.Bool(false), err
		}
		v, err := m.sigValue(*p)
		if err != nil {
			return eval.Value{}, err
		}
		return eval.Bool((v.I != 0) == (x.Name == "rising_edge")), nil
	case "to_string":
		return m.toString(args, rt)
	case "to_ostring", "to_hstring":
		bits := 3
		if x.Name == "to_hstring" {
			bits = 4
		}
		return strValue(rt, radixString(args[0].v, bits)), nil
	}
	return eval.Value{}, unsupported(x)
}

func strValue(t *eval.Type, s string) eval.Value {
	v := eval.Str(s)
	if t != nil {
		v.T = t
		if !t.Constrained() || len(v.E) == 0 {
			v.T = t
		}
	}
	return v
}

// toString is the implicit TO_STRING (LRM 5.7): 'IMAGE of a scalar
// (a character without quotes), the characters of an array of
// character literals, a time in a unit, a real with digits or a
// format.
func (m *machine) toString(args []arg, rt *eval.Type) (eval.Value,
	error,
) {
	v := args[0].v
	if len(args) == 2 {
		switch v.Kind() {
		case eval.KindPhysical:
			return strValue(rt, timeText(v, args[1].v.I)), nil
		case eval.KindFloating:
			if args[1].v.Kind() == eval.KindArray {
				return strValue(rt, formatReal(v.F,
					stringOf(args[1].v))), nil
			}
			return strValue(rt, realText(v.F, args[1].v.I)), nil
		}
	}
	switch v.Kind() {
	case eval.KindArray:
		var sb strings.Builder
		for _, e := range v.E {
			sb.WriteString(charOf(e))
		}
		return strValue(rt, sb.String()), nil
	case eval.KindEnum:
		return strValue(rt, charOf(v)), nil
	case eval.KindFloating:
		return strValue(rt, realText(v.F, 0)), nil
	}
	return strValue(rt, v.Image()), nil
}

// charOf is the text of an enumeration value: a character literal
// without its quotes, an identifier as declared.
func charOf(v eval.Value) string {
	s := v.Image()
	if len(s) == 3 && s[0] == '\'' && s[2] == '\'' {
		return s[1:2]
	}
	return s
}

// radixString is TO_OSTRING / TO_HSTRING of a BIT_VECTOR: groups of
// bits from the right, the leftmost group zero-extended.
func radixString(v eval.Value, bits int) string {
	n := len(v.E)
	digits := (n + bits - 1) / bits
	out := make([]byte, digits)
	for d := range digits {
		val := 0
		for k := range bits {
			i := n - 1 - (d*bits + k)
			if i >= 0 && v.E[i].I != 0 {
				val |= 1 << k
			}
		}
		out[digits-1-d] = "0123456789ABCDEF"[val]
	}
	return string(out)
}

// timeText writes time v in the unit of size unit (fs).
func timeText(v eval.Value, unit int64) string {
	name := unitName(v.T, unit)
	if unit <= 0 {
		unit, name = 1, "fs"
	}
	if v.I%unit == 0 {
		return strconv.FormatInt(v.I/unit, 10) + " " + name
	}
	q, r := v.I/unit, v.I%unit
	if r < 0 {
		r = -r
	}
	frac := strconv.FormatInt(r, 10)
	width := len(strconv.FormatInt(unit, 10)) - 1
	for len(frac) < width {
		frac = "0" + frac
	}
	frac = strings.TrimRight(frac, "0")
	s := strconv.FormatInt(q, 10)
	if q == 0 && v.I < 0 {
		s = "-0"
	}
	return s + "." + frac + " " + name
}

func unitName(t *eval.Type, f int64) string {
	if t == nil {
		return "fs"
	}
	for _, u := range t.BaseType().Units {
		if u.Factor == f {
			return u.Name
		}
	}
	return "fs"
}

func realText(f float64, digits int64) string {
	if digits <= 0 {
		return strconv.FormatFloat(f, 'e', -1, 64)
	}
	return strconv.FormatFloat(f, 'f', int(digits), 64)
}

// formatReal applies a C-style format such as "%8.3f" (the subset
// %[w][.p](f|e|g)).
func formatReal(f float64, format string) string {
	s := strings.TrimPrefix(format, "%")
	if s == "" {
		return realText(f, 0)
	}
	verb := s[len(s)-1]
	s = s[:len(s)-1]
	prec := -1
	width := 0
	if i := strings.IndexByte(s, '.'); i >= 0 {
		prec, _ = strconv.Atoi(s[i+1:])
		s = s[:i]
	}
	width, _ = strconv.Atoi(s)
	switch verb {
	case 'f', 'e', 'g', 'E', 'G':
	default:
		verb = 'g'
	}
	out := strconv.FormatFloat(f, verb, prec, 64)
	for len(out) < width {
		out = " " + out
	}
	return out
}

// justify pads s to field (side 0: right-justified, 1: left).
func justify(s string, side, field int64) string {
	for int64(len(s)) < field {
		if side == 1 {
			s += " "
		} else {
			s = " " + s
		}
	}
	return s
}

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

// builtinProc executes a built-in procedure; stop reports
// std.env.stop or finish.
func (m *machine) builtinProc(n *node, x *sema.Decl, args []arg,
) (stop bool, err error) {
	switch {
	case x.Implicit:
	case unitOf(x) == "std.env":
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
	case unitOf(x) == "std.textio":
		return false, m.textio(x, args)
	default:
		return false, unsupported(x)
	}
	switch x.Name {
	case "deallocate":
		p := args[0]
		if p.v.I != 0 {
			m.w.free(p.v)
		}
		return false, m.out(p, eval.Value{T: p.v.T})
	case "file_open":
		return false, m.fileOpen(args)
	case "file_close":
		return false, m.fileClose(args[0].v)
	case "flush":
		return false, nil
	case "write":
		f, err := m.file(args[0].v)
		if err != nil {
			return false, err
		}
		return false, m.writeText(f, stringOf(args[1].v)+"\n")
	case "read":
		f, err := m.file(args[0].v)
		if err != nil {
			return false, err
		}
		line, ok := f.readLine()
		if !ok {
			return false, errors.New("read past the end of a file")
		}
		return false, m.out(args[1], strValue(args[1].v.T, line))
	}
	return false, unsupported(x)
}

// out assigns v to the actual of out or inout parameter a.
func (m *machine) out(a arg, v eval.Value) error {
	if a.dst == nil {
		return nil
	}
	return m.assignPlace(*a.dst, v)
}

// File states.
const (
	modeRead   = 0
	modeWrite  = 1
	modeAppend = 2
)

// fileState is one file object.
type fileState struct {
	name string
	open bool
	mode int64
	std  int // 1: std.textio.INPUT, 2: OUTPUT
	data []byte
	pos  int
}

func (f *fileState) readLine() (string, bool) {
	if f.pos >= len(f.data) {
		return "", false
	}
	rest := f.data[f.pos:]
	i := strings.IndexByte(string(rest), '\n')
	var line string
	if i < 0 {
		line, f.pos = string(rest), len(f.data)
	} else {
		line, f.pos = string(rest[:i]), f.pos+i+1
	}
	return strings.TrimSuffix(line, "\r"), true
}

func (m *machine) file(v eval.Value) (*fileState, error) {
	f, ok := m.w.files[v.I]
	if !ok {
		return nil, errors.New("not a file object")
	}
	return f, nil
}

// fileObject elaborates the file declaration d (LRM 6.4.2.5): a file
// object, opened when the declaration has open information.
func (m *machine) fileObject(d *sema.Decl) (slot, error) {
	t, err := m.ev.LayoutIn(d.Type, m.b)
	if err != nil {
		return slot{}, err
	}
	h := m.w.handle()
	f := &fileState{}
	m.w.files[h] = f
	s := slot{v: eval.Value{T: t, I: h}, t: t}
	if d.Node.Kind() != cst.KindFileDecl {
		return s, nil
	}
	n := cst.FileDecl{Node: d.Node}.OpenInfo().Node
	if n.IsZero() {
		return s, nil
	}
	nameNode := childAfter(n, token.KwIs)
	if nameNode.IsZero() {
		return s, nil
	}
	mode := int64(modeRead)
	if k := childAfter(n, token.KwOpen); !k.IsZero() {
		switch strings.ToLower(strings.TrimSpace(k.Text())) {
		case "write_mode":
			mode = modeWrite
		case "append_mode":
			mode = modeAppend
		case "read_mode":
		default:
			return slot{}, errors.New("the open kind " + k.Text() +
				" must be read_mode, write_mode or append_mode")
		}
	}
	name, err := m.literalName(d, nameNode)
	if err != nil {
		return slot{}, err
	}
	switch strings.ToUpper(name) {
	case "STD_INPUT":
		f.std, f.open, f.mode = 1, true, modeRead
		f.data = m.w.o.Input
		return s, nil
	case "STD_OUTPUT":
		f.std, f.open, f.mode = 2, true, modeWrite
		return s, nil
	}
	if status := m.w.open(f, name, mode); status != 0 {
		return slot{}, errors.New("cannot open file " +
			strconv.Quote(name))
	}
	return s, nil
}

// literalName returns the value of the logical name n of a file
// declaration: a string expression (LRM 6.4.2.5).
func (m *machine) literalName(d *sema.Decl, n cst.Node) (string, error) {
	text := strings.TrimSpace(n.Text())
	if len(text) >= 2 && text[0] == '"' && text[len(text)-1] == '"' &&
		n.Kind() == cst.KindLiteralExpr {
		return strings.ReplaceAll(text[1:len(text)-1], `""`, `"`), nil
	}
	if e := m.w.prog.expr(d.File, n); e != nil {
		v, err := m.ev.Eval(e, m.b)
		if err != nil {
			return "", err
		}
		return stringOf(v), nil
	}
	if f := d.File; f != nil {
		key := sema.Key(strings.ToLower(text))
		for _, c := range m.w.d.r.LookupAt(f.Path, d.Span.Start, key) {
			c = c.Denotes()
			if c.Kind == sema.DeclConstant || c.Kind == sema.DeclGeneric {
				v, err := m.ev.Eval(&sema.Expr{Kind: sema.ExprName,
					Decl: c, Type: c.Type}, m.b)
				if err != nil {
					return "", err
				}
				return stringOf(v), nil
			}
		}
	}
	return "", errors.New("the file name " + text + " must be a string " +
		"literal or constant")
}

// open opens f on name in mode and returns the FILE_OPEN_STATUS
// position: 0 open_ok, 1 status_error, 2 name_error, 3 mode_error.
func (w *world) open(f *fileState, name string, mode int64) int64 {
	if f.open {
		return 1
	}
	switch strings.ToUpper(name) {
	case "STD_INPUT":
		if mode != modeRead {
			return 3
		}
		*f = fileState{name: name, open: true, mode: mode, std: 1,
			data: w.o.Input}
		return 0
	case "STD_OUTPUT":
		if mode == modeRead {
			return 3
		}
		*f = fileState{name: name, open: true, mode: mode, std: 2}
		return 0
	}
	var data []byte
	switch mode {
	case modeRead, modeAppend:
		if b, ok := w.disk[name]; ok {
			data = append([]byte(nil), b...)
		} else if w.o.FS != nil {
			b, err := w.o.FS.ReadFile(name)
			if err != nil && mode == modeRead {
				return 2
			}
			data = b
		} else if mode == modeRead {
			return 2
		}
	case modeWrite:
	default:
		return 3
	}
	*f = fileState{name: name, open: true, mode: mode, data: data}
	return 0
}

func (m *machine) fileOpen(args []arg) error {
	i := 0
	var status *arg
	if len(args) == 4 {
		status, i = &args[0], 1
	}
	f, err := m.file(args[i].v)
	if err != nil {
		return err
	}
	name := stringOf(args[i+1].v)
	mode := int64(modeRead)
	if a := args[i+2]; a.v.T != nil {
		mode = a.v.I
	}
	st := m.w.open(f, name, mode)
	if status == nil {
		if st != 0 {
			return errors.New("cannot open file " + strconv.Quote(name))
		}
		return nil
	}
	return m.out(*status, eval.Value{T: status.v.T, I: st})
}

func (m *machine) fileClose(v eval.Value) error {
	f, err := m.file(v)
	if err != nil {
		return err
	}
	if !f.open {
		return nil
	}
	f.open = false
	if f.std != 0 || f.mode == modeRead {
		return nil
	}
	m.w.disk[f.name] = append([]byte(nil), f.data...)
	if fs := m.w.o.FS; fs != nil {
		if err := fs.WriteFile(f.name, f.data, 0o644); err != nil {
			return errors.New("cannot write file " +
				strconv.Quote(f.name) + ": " + err.Error())
		}
	}
	return nil
}

// writeText appends s to the open output file f.
func (m *machine) writeText(f *fileState, s string) error {
	if !f.open || f.mode == modeRead {
		return errors.New("write to a file not open for writing")
	}
	if f.std == 2 {
		if out := m.w.o.Output; out != nil {
			out(strings.TrimSuffix(s, "\n"))
		}
		return nil
	}
	f.data = append(f.data, s...)
	return nil
}
