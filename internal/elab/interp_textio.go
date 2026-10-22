package elab

// interp_textio.go is std.textio (LRM 16.4), built into the kernel: a
// LINE is an access value whose object is the line's text; READ
// procedures consume from its left end, WRITE procedures append.

import (
	"errors"
	"strconv"
	"strings"

	"github.com/connerohnesorge/gohard/internal/eval"
	"github.com/connerohnesorge/gohard/internal/sema"
	"github.com/connerohnesorge/gohard/internal/sim"
	"github.com/connerohnesorge/gohard/internal/token"
)

// line returns the text of LINE value l ("" for null).
func (m *machine) line(l eval.Value) (string, eval.Value, error) {
	if l.I == 0 {
		return "", eval.Value{}, nil
	}
	v, err := m.w.deref(l)
	if err != nil {
		return "", eval.Value{}, err
	}
	return stringOf(v), v, nil
}

// setLine makes line parameter a designate the text s.
func (m *machine) setLine(a arg, s string) error {
	v := eval.Str(s)
	if a.v.I != 0 {
		if old, ok := m.w.heap[a.v.I]; ok && old.T != nil {
			v.T = old.T
		}
		m.w.heap[a.v.I] = v
		return nil
	}
	return m.out(a, m.w.alloc(a.v.T, v))
}

// consume drops the first k characters of line parameter a, keeping
// the index of the characters that remain.
func (m *machine) consume(a arg, k int) error {
	if k == 0 || a.v.I == 0 {
		return nil
	}
	_, v, err := m.line(a.v)
	if err != nil {
		return err
	}
	out := v
	out.E = v.E[k:]
	if len(v.B) == 1 {
		r := v.B[0]
		if r.Dir == eval.To {
			r.Left += int64(k)
		} else {
			r.Left -= int64(k)
		}
		out.B = []eval.Range{r}
	}
	m.w.heap[a.v.I] = out
	return nil
}

func valueKind(x *sema.Decl) string {
	if len(x.Params) < 2 || x.Params[1].Type == nil {
		return ""
	}
	if d := x.Params[1].Type.Base().Decl; d != nil {
		return string(d.Name)
	}
	return ""
}

// textio runs a std.textio procedure.
func (m *machine) textio(x *sema.Decl, args []arg) error {
	switch x.Name {
	case "readline":
		f, err := m.file(args[0].v)
		if err != nil {
			return err
		}
		if !f.open || f.mode != modeRead {
			return errors.New("readline from a file not open for " +
				"reading")
		}
		s, ok := f.readLine()
		if !ok {
			return errors.New("readline past the end of the file")
		}
		if args[1].v.I != 0 {
			m.w.free(args[1].v)
		}
		return m.out(args[1], m.w.alloc(args[1].v.T, eval.Str(s)))
	case "writeline", "tee":
		f, err := m.file(args[0].v)
		if err != nil {
			return err
		}
		s, _, err := m.line(args[1].v)
		if err != nil {
			return err
		}
		if err := m.writeText(f, s+"\n"); err != nil {
			return err
		}
		if x.Name == "tee" {
			if out := m.w.o.Output; out != nil {
				out(s)
			}
		}
		return m.setLine(args[1], "")
	case "write", "owrite", "hwrite":
		return m.textWrite(x, args)
	case "read", "oread", "hread", "sread":
		return m.textRead(x, args)
	}
	return unsupported(x)
}

func (m *machine) textWrite(x *sema.Decl, args []arg) error {
	l := args[0]
	cur, _, err := m.line(l.v)
	if err != nil {
		return err
	}
	v := args[1].v
	var s string
	side, field := int64(0), int64(0)
	if len(args) > 2 && args[2].v.T != nil &&
		args[2].v.Kind() == eval.KindEnum {
		side = args[2].v.I
	}
	if len(args) > 3 {
		field = args[3].v.I
	}
	switch x.Name {
	case "owrite":
		s = radixString(v, 3)
	case "hwrite":
		s = radixString(v, 4)
	default:
		switch valueKind(x) {
		case "bit", "character":
			s = charOf(v)
		case "bit_vector":
			var sb strings.Builder
			for _, e := range v.E {
				sb.WriteString(charOf(e))
			}
			s = sb.String()
		case "boolean":
			s = strings.ToUpper(v.Image())
		case "integer":
			s = strconv.FormatInt(v.I, 10)
		case "real":
			if len(args) == 3 && args[2].v.Kind() == eval.KindArray {
				s = formatReal(v.F, stringOf(args[2].v))
				side, field = 0, 0
			} else {
				digits := int64(0)
				if len(args) > 4 {
					digits = args[4].v.I
				}
				s = realText(v.F, digits)
			}
		case "string":
			s = stringOf(v)
		case "time":
			unit := int64(1000000) // ns
			if len(args) > 4 {
				unit = args[4].v.I
			}
			s = timeText(v, unit)
		default:
			return unsupported(x)
		}
	}
	return m.setLine(l, cur+justify(s, side, field))
}

func isSpace(c byte) bool { return c == ' ' || c == '\t' || c == 0xa0 }

// skip returns the number of leading white-space characters of s.
func skip(s string) int {
	i := 0
	for i < len(s) && isSpace(s[i]) {
		i++
	}
	return i
}

func (m *machine) textRead(x *sema.Decl, args []arg) error {
	l := args[0]
	s, _, err := m.line(l.v)
	if err != nil {
		return err
	}
	val := args[1]
	var good *arg
	if len(args) > 2 && x.Name != "sread" {
		good = &args[2]
	}
	v, used, ok := m.parse(x, s, val.v)
	if x.Name == "sread" {
		i := skip(s)
		j := i
		for j < len(s) && !isSpace(s[j]) && j-i < len(val.v.E) {
			j++
		}
		str := s[i:j] + strings.Repeat(" ", len(val.v.E)-(j-i))
		nv := strValue(val.v.T, str)
		nv.B = val.v.B
		if err := m.out(val, nv); err != nil {
			return err
		}
		if err := m.out(args[2], eval.Value{T: args[2].v.T,
			I: int64(j - i)}); err != nil {
			return err
		}
		return m.consume(l, j)
	}
	if good != nil {
		if err := m.out(*good, eval.Value{T: good.v.T,
			I: boolInt(ok)}); err != nil {
			return err
		}
	} else if !ok {
		return m.readFailed(x, s)
	}
	if !ok {
		return nil
	}
	if err := m.out(val, v); err != nil {
		return err
	}
	return m.consume(l, used)
}

// readFailed handles a READ without GOOD that fails on s. In
// VHDL-2019 it is an assertion violation of the GetVhdlReadSeverity
// level (LRM 16.4, 16.5.4), worded as NVC 1.23 does, and the read
// leaves the value and the line as they were; the run goes on unless
// that level reaches simulation.stop_level. Before VHDL-2019 it stays
// the v1.0 run-time error.
func (m *machine) readFailed(x *sema.Decl, s string) error {
	if m.ctx == nil || m.ev.Standard() < token.Std2019 {
		return errors.New("textio read of " + valueKind(x) +
			" failed on " + strconv.Quote(s))
	}
	var src sim.SrcRef
	if n := m.current(); n != nil {
		src = n.src
	} else if ps := m.w.d.Model.Processes; m.idx >= 0 &&
		m.idx < len(ps) {
		src = ps[m.idx].Src
	}
	return m.ctx.Report(sim.Report{Assertion: true,
		Severity: m.ctx.ReadSeverity(),
		Message:  valueKind(x) + " read failed", Src: src})
}

func boolInt(b bool) int64 {
	if b {
		return 1
	}
	return 0
}

// parse reads a value like cur (the out actual) from the start of s:
// the value, the characters used, and whether it succeeded.
func (m *machine) parse(x *sema.Decl, s string, cur eval.Value) (
	eval.Value, int, bool,
) {
	switch x.Name {
	case "hread", "oread":
		bits := 4
		if x.Name == "oread" {
			bits = 3
		}
		return parseRadix(s, cur, bits)
	}
	switch valueKind(x) {
	case "character":
		if len(s) == 0 {
			return cur, 0, false
		}
		return eval.Value{T: cur.T, I: int64(s[0])}, 1, true
	case "string":
		n := len(cur.E)
		if len(s) < n {
			return cur, 0, false
		}
		out := cur
		out.E = make([]eval.Value, n)
		for i := range n {
			out.E[i] = eval.Value{T: elemType(cur), I: int64(s[i])}
		}
		return out, n, true
	case "bit":
		i := skip(s)
		if i < len(s) && (s[i] == '0' || s[i] == '1') {
			return eval.Value{T: cur.T, I: int64(s[i] - '0')}, i + 1, true
		}
		return cur, 0, false
	case "bit_vector":
		i := skip(s)
		out := cur
		out.E = make([]eval.Value, len(cur.E))
		k := 0
		for k < len(out.E) && i < len(s) {
			c := s[i]
			switch {
			case c == '0' || c == '1':
				out.E[k] = eval.Value{T: elemType(cur), I: int64(c - '0')}
				k++
			case c == '_' && k > 0:
			default:
				return cur, 0, false
			}
			i++
		}
		return out, i, k == len(out.E)
	case "boolean":
		i := skip(s)
		j := i
		for j < len(s) && isLetter(s[j]) {
			j++
		}
		switch strings.ToLower(s[i:j]) {
		case "true":
			return eval.Value{T: cur.T, I: 1}, j, true
		case "false":
			return eval.Value{T: cur.T, I: 0}, j, true
		}
		return cur, 0, false
	case "integer":
		i := skip(s)
		j := i
		if j < len(s) && (s[j] == '-' || s[j] == '+') {
			j++
		}
		k := j
		for k < len(s) && (isDigit(s[k]) || s[k] == '_' && k > j) {
			k++
		}
		if k == j {
			return cur, 0, false
		}
		n, err := strconv.ParseInt(strings.ReplaceAll(s[i:k], "_", ""),
			10, 64)
		if err != nil || cur.T != nil && !cur.T.Range.Contains(n) {
			return cur, 0, false
		}
		return eval.Value{T: cur.T, I: n}, k, true
	case "real":
		i := skip(s)
		j := scanReal(s, i)
		if j == i {
			return cur, 0, false
		}
		f, err := strconv.ParseFloat(strings.ReplaceAll(s[i:j], "_", ""),
			64)
		if err != nil {
			return cur, 0, false
		}
		return eval.Value{T: cur.T, F: f}, j, true
	case "time":
		i := skip(s)
		j := scanReal(s, i)
		if j == i {
			return cur, 0, false
		}
		num := strings.ReplaceAll(s[i:j], "_", "")
		k := j + skip(s[j:])
		e := k
		for e < len(s) && isLetter(s[e]) {
			e++
		}
		if e == k || cur.T == nil {
			return cur, 0, false
		}
		unit := strings.ToLower(s[k:e])
		for _, u := range cur.T.BaseType().Units {
			if u.Name != unit {
				continue
			}
			if n, err := strconv.ParseInt(num, 10, 64); err == nil {
				return eval.Value{T: cur.T, I: n * u.Factor}, e, true
			}
			f, err := strconv.ParseFloat(num, 64)
			if err != nil {
				return cur, 0, false
			}
			return eval.Value{T: cur.T, I: int64(f*float64(u.Factor) +
				0.5)}, e, true
		}
		return cur, 0, false
	}
	return cur, 0, false
}

func elemType(v eval.Value) *eval.Type {
	if v.T != nil && v.T.Elem != nil {
		return v.T.Elem
	}
	if len(v.E) > 0 {
		return v.E[0].T
	}
	return nil
}

func isDigit(c byte) bool  { return c >= '0' && c <= '9' }
func isLetter(c byte) bool { return c|0x20 >= 'a' && c|0x20 <= 'z' }

// scanReal returns the end of the decimal number at s[i:].
func scanReal(s string, i int) int {
	j := i
	if j < len(s) && (s[j] == '-' || s[j] == '+') {
		j++
	}
	d := j
	for j < len(s) && (isDigit(s[j]) || s[j] == '_' && j > d) {
		j++
	}
	if j == d {
		return i
	}
	if j+1 < len(s) && s[j] == '.' && isDigit(s[j+1]) {
		j++
		for j < len(s) && (isDigit(s[j]) || s[j] == '_') {
			j++
		}
	}
	if j < len(s) && (s[j] == 'e' || s[j] == 'E') {
		k := j + 1
		if k < len(s) && (s[k] == '-' || s[k] == '+') {
			k++
		}
		if k < len(s) && isDigit(s[k]) {
			j = k
			for j < len(s) && isDigit(s[j]) {
				j++
			}
		}
	}
	return j
}

// parseRadix is HREAD and OREAD of a BIT_VECTOR (LRM 16.4): digits
// (underscores between them) for exactly the value's length; the
// excess leftmost bits must be zero.
func parseRadix(s string, cur eval.Value, bits int) (eval.Value, int,
	bool,
) {
	n := len(cur.E)
	digits := (n + bits - 1) / bits
	i := skip(s)
	var vals []int
	for i < len(s) && len(vals) < digits {
		c := s[i]
		d := strings.IndexByte("0123456789abcdef", c|0x20)
		switch {
		case c == '_' && len(vals) > 0:
		case d >= 0 && d < 1<<bits:
			vals = append(vals, d)
		default:
			return cur, 0, false
		}
		i++
	}
	if len(vals) < digits {
		return cur, 0, false
	}
	out := cur
	out.E = make([]eval.Value, n)
	et := elemType(cur)
	for k := range digits * bits {
		d := vals[digits-1-k/bits]
		bit := int64(d>>(k%bits)) & 1
		pos := n - 1 - k
		if pos < 0 {
			if bit != 0 {
				return cur, 0, false
			}
			continue
		}
		out.E[pos] = eval.Value{T: et, I: bit}
	}
	return out, i, true
}
