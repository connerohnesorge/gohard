package elab

// interp_implicit.go holds the implicit operations the kernel runs
// natively (#35 R6): TO_STRING, TO_OSTRING, TO_HSTRING, ENDFILE,
// RISING_EDGE and FALLING_EDGE of BIT and BOOLEAN, DEALLOCATE and the
// file operations, with the text helpers std.textio shares. Files go
// through world (in memory) and InterpOptions.FS only.

import (
	"errors"
	"io/fs"
	"maps"
	"slices"
	"strconv"
	"strings"

	"github.com/connerohnesorge/gohard/v2/internal/cst"
	"github.com/connerohnesorge/gohard/v2/internal/eval"
	"github.com/connerohnesorge/gohard/v2/internal/sema"
	"github.com/connerohnesorge/gohard/v2/internal/token"
)

// implicitFunc evaluates an implicit function without an evaluator
// operation.
func (m *machine) implicitFunc(fn *callFn, args []arg) (eval.Value,
	error,
) {
	x := fn.x
	rt := fn.ret
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

// implicitProc executes an implicit procedure.
func (m *machine) implicitProc(x *sema.Decl, args []arg) error {
	switch x.Name {
	case "deallocate":
		p := args[0]
		if p.v.I != 0 {
			m.w.free(p.v)
		}
		return m.out(p, eval.Value{T: p.v.T})
	case "file_open":
		return m.fileOpen(args)
	case "file_close":
		return m.fileClose(args[0].v)
	case "flush":
		return nil
	case "write":
		if !textValue(args[1].v) {
			return unsupported(x)
		}
		f, err := m.file(args[0].v)
		if err != nil {
			return err
		}
		return m.writeText(f, stringOf(args[1].v)+"\n")
	case "read":
		if !textValue(args[1].v) {
			return unsupported(x)
		}
		f, err := m.file(args[0].v)
		if err != nil {
			return err
		}
		line, ok := f.readLine()
		if !ok {
			return errors.New("read past the end of a file")
		}
		return m.out(args[1], strValue(args[1].v.T, line))
	}
	return unsupported(x)
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
		if !charArray(v.T) {
			// VHDL-2019: an array of another scalar type is written
			// as its 'IMAGE, (5,6) (as NVC 1.23).
			return strValue(rt, eval.ImageOf(v)), nil
		}
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

// charArray reports whether t is an array of a character type (an
// enumeration type with a character literal, LRM 5.2.2.1): its
// TO_STRING is its characters (VHDL-2008).
func charArray(t *eval.Type) bool {
	if t == nil {
		return true // a value without a layout: as before
	}
	e := t.BaseType().Elem
	if e == nil || e.Kind != eval.KindEnum {
		return false
	}
	for _, l := range e.BaseType().Literals {
		if strings.HasPrefix(l, "'") {
			return true
		}
	}
	return false
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

// out assigns v to the actual of out or inout parameter a.
func (m *machine) out(a arg, v eval.Value) error {
	if a.dst == nil {
		return nil
	}
	return m.assignPlace(*a.dst, v)
}

// File states.
const (
	modeRead      = 0
	modeWrite     = 1
	modeAppend    = 2
	modeReadWrite = 3 // VHDL-2019 READ_WRITE_MODE: no native yet
)

// openKind maps a FILE_OPEN_KIND value to its mode by literal name:
// the bundled std.standard declares READ_WRITE_MODE second under every
// standard, so the positions of WRITE_MODE and APPEND_MODE are not the
// mode numbers.
func openKind(v eval.Value) int64 {
	if v.T == nil {
		return modeRead
	}
	t := v.T
	if t.Base != nil {
		t = t.Base
	}
	if lits := t.Literals; v.I >= 0 && v.I < int64(len(lits)) {
		switch strings.ToLower(lits[v.I]) {
		case "read_mode":
			return modeRead
		case "write_mode":
			return modeWrite
		case "append_mode":
			return modeAppend
		case "read_write_mode":
			return modeReadWrite
		}
	}
	return v.I
}

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
	if mode != modeRead && mode != modeWrite && mode != modeAppend {
		return 3
	}
	name = w.inCwd(name)
	if w.denied(name) {
		return 2
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
	}
	*f = fileState{name: name, open: true, mode: mode, data: data}
	return 0
}

// denied reports whether the file door refuses name outright: a name
// outside the sandbox (outside the project root, or inside .gohard,
// see env.Overlay), or a directory (the empty name is the root), is
// NAME_ERROR when it is opened in any mode, not a failure when it is
// closed, and never a write dropped silently.
func (w *world) denied(name string) bool {
	if w.o.FS == nil {
		return false
	}
	fi, err := w.o.FS.Stat(name)
	return errors.Is(err, fs.ErrPermission) || err == nil && fi.IsDir()
}

// textValue reports whether v, the value an implicit READ or WRITE
// moves, is text: a STRING, the element type of std.textio.TEXT and
// the only one the file operations support. A file of another type (a
// file of INTEGER) is a construct GoHard does not simulate yet:
// unsupported/construct, never a wrong value read or written silently.
func textValue(v eval.Value) bool {
	if v.T == nil {
		return false
	}
	e := v.T.BaseType()
	return e.Kind == eval.KindArray && len(e.Index) == 1 &&
		e.Elem != nil && e.Elem.BaseType().Name == "CHARACTER"
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
		mode = openKind(a.v)
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
	return m.w.close(f)
}

// close closes f; the contents of a file open for writing reach the
// file door.
func (w *world) close(f *fileState) error {
	if !f.open {
		return nil
	}
	f.open = false
	if f.std != 0 || f.mode == modeRead {
		return nil
	}
	w.disk[f.name] = append([]byte(nil), f.data...)
	if door := w.o.FS; door != nil {
		if err := door.WriteFile(f.name, f.data, 0o644); err != nil {
			return errors.New("cannot write file " +
				strconv.Quote(f.name) + ": " + err.Error())
		}
	}
	return nil
}

// closeAll closes every file still open at the end of the run, in
// handle (creation) order: LRM 5.5.2 closes them implicitly, so a file
// the design never closed is written like one it closed.
func (w *world) closeAll() error {
	var errs []error
	for _, h := range slices.Sorted(maps.Keys(w.files)) {
		if err := w.close(w.files[h]); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
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
