package trace

import (
	"bytes"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// Magic is the first line of the canonical text form.
const Magic = "gohard-trace 1"

// Encode returns the canonical text form of t (see the package
// documentation for the grammar). t must be valid.
func Encode(t *Trace) []byte {
	var b bytes.Buffer
	b.WriteString(Magic + "\n")
	line(&b, "testbench", tok(t.Header.Testbench))
	line(&b, "end", strconv.FormatUint(uint64(t.Header.End), 10))
	if t.Header.NonReproducible {
		line(&b, "reproducible", "no")
	}
	for i := range t.Types {
		encodeType(&b, &t.Types[i])
	}
	for _, s := range t.Scopes {
		f := []string{tok(s.Path), s.Kind.String()}
		if s.Unit != "" {
			f = append(f, tok(s.Unit))
		}
		line(&b, "scope", f...)
	}
	for i := range t.Signals {
		s := &t.Signals[i]
		f := []string{tok(s.Path), tok(s.Type), s.Dir.String()}
		if t.SignalType(i).Kind == Array {
			to := "to"
			if s.Downto {
				to = "downto"
			}
			f = append(f, itoa(s.Left), to, itoa(s.Right))
		}
		line(&b, "signal", f...)
	}
	for i, p := range t.Processes {
		f := []string{strconv.Itoa(i + 1), tok(p.Path)}
		if p.Modeled {
			f = append(f, "modeled")
		}
		line(&b, "process", f...)
	}
	ci, ei := 0, 0
	for ci < len(t.Changes) || ei < len(t.Events) {
		if ci < len(t.Changes) && (ei == len(t.Events) ||
			!eventFirst(&t.Events[ei], &t.Changes[ci])) {
			encodeChange(&b, t, &t.Changes[ci])
			ci++
			continue
		}
		encodeEvent(&b, &t.Events[ei])
		ei++
	}
	return b.Bytes()
}

// eventFirst reports whether e is written before c: entries go by
// (time, delta), changes before events.
func eventFirst(e *Event, c *Change) bool {
	if e.Time != c.Time {
		return e.Time < c.Time
	}
	return e.Delta < c.Delta
}

func line(b *bytes.Buffer, head string, f ...string) {
	b.WriteString(head)
	for _, s := range f {
		b.WriteByte(' ')
		b.WriteString(s)
	}
	b.WriteByte('\n')
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }

// tok writes s as one token: as is when it is printable ASCII without
// spaces and does not start with a double quote, else Go-quoted.
func tok(s string) string {
	if s == "" || s[0] == '"' {
		return strconv.Quote(s)
	}
	for i := range len(s) {
		if s[i] <= ' ' || s[i] > '~' {
			return strconv.Quote(s)
		}
	}
	return s
}

func encodeType(b *bytes.Buffer, ty *Type) {
	f := []string{tok(ty.Name), ty.Kind.String()}
	switch ty.Kind {
	case Enum:
		for _, l := range ty.Literals {
			f = append(f, tok(l))
		}
	case Integer:
		f = append(f, itoa(ty.Low), itoa(ty.High))
	case Physical:
		f = append(f, itoa(ty.Low), itoa(ty.High))
		for _, u := range ty.Units {
			f = append(f, tok(u.Name), itoa(u.Mult))
		}
	case Array:
		f = append(f, tok(ty.Elem))
	}
	line(b, "type", f...)
}

func encodeChange(b *bytes.Buffer, t *Trace, c *Change) {
	f := []string{
		strconv.FormatUint(uint64(c.Time), 10),
		strconv.FormatUint(uint64(c.Delta), 10),
		tok(t.Signals[c.Signal].Path),
	}
	f = appendValue(f, t, t.SignalType(c.Signal), c.Value)
	f = append(f, procTok(c.Process))
	if c.Modeled {
		f = append(f, "modeled")
	}
	b.WriteString(strings.Join(f, " "))
	b.WriteByte('\n')
}

func procTok(p int) string {
	if p == 0 {
		return "-"
	}
	return "p" + strconv.Itoa(p)
}

// FormatValue returns the canonical text of v as a value of ty (the
// same text the trace file uses), for hovers and messages.
func (t *Trace) FormatValue(ty *Type, v Value) string {
	return strings.Join(appendValue(nil, t, ty, v), " ")
}

func appendValue(f []string, t *Trace, ty *Type, v Value) []string {
	switch ty.Kind {
	case Logic:
		return append(f, string(v.Logic()))
	case Enum:
		return append(f, tok(ty.Literals[v.Int]))
	case Integer, Physical:
		return append(f, itoa(v.Int))
	case Real:
		return append(f, strconv.FormatFloat(v.Real, 'g', -1, 64))
	}
	et := t.ElemType(ty)
	if et.Kind == Logic {
		s := make([]byte, len(v.Elems))
		for i, e := range v.Elems {
			s[i] = e.Logic()
		}
		return append(f, tok(string(s)))
	}
	f = append(f, "[")
	for _, e := range v.Elems {
		f = appendValue(f, t, et, e)
	}
	return append(f, "]")
}

func encodeEvent(b *bytes.Buffer, e *Event) {
	line(b, strconv.FormatUint(uint64(e.Time), 10),
		strconv.FormatUint(uint64(e.Delta), 10), "!", e.Kind.String(),
		e.Severity.String(), tok(e.File+":"+strconv.Itoa(e.Line)),
		procTok(e.Process), strconv.Quote(e.Message))
}

// Decode parses the canonical text form. It accepts only canonical
// input: Encode(Decode(data)) is data, byte for byte. Errors name the
// line.
func Decode(data []byte) (*Trace, error) {
	t, err := decode(data)
	if err != nil {
		return nil, err
	}
	if err := t.Validate(); err != nil {
		return nil, err
	}
	if back := Encode(t); !bytes.Equal(back, data) {
		n := firstDiff(data, back)
		return nil, fmt.Errorf("line %d: not in canonical form", n)
	}
	return t, nil
}

// firstDiff returns the one-based line of the first difference of a and
// b.
func firstDiff(a, b []byte) int {
	n := 1
	for i := 0; i < len(a) && i < len(b) && a[i] == b[i]; i++ {
		if a[i] == '\n' {
			n++
		}
	}
	return n
}

// FirstDiff returns the one-based line number and the two lines where
// canonical traces a and b first differ, for golden mismatches (#36: CI
// shows the first differing line). ok is false when they are equal.
func FirstDiff(a, b []byte) (n int, la, lb string, ok bool) {
	if bytes.Equal(a, b) {
		return 0, "", "", false
	}
	n = firstDiff(a, b)
	return n, nthLine(a, n), nthLine(b, n), true
}

func nthLine(data []byte, n int) string {
	for range n - 1 {
		i := bytes.IndexByte(data, '\n')
		if i < 0 {
			return ""
		}
		data = data[i+1:]
	}
	if i := bytes.IndexByte(data, '\n'); i >= 0 {
		data = data[:i]
	}
	return string(data)
}

type parser struct {
	t    *Trace
	line int
	f    []string // tokens of the current line
	k    int      // next token
}

func (p *parser) errf(format string, args ...any) error {
	return fmt.Errorf("line %d: %s", p.line, fmt.Sprintf(format, args...))
}

func decode(data []byte) (*Trace, error) {
	if len(data) == 0 || data[len(data)-1] != '\n' {
		return nil, errors.New("line 1: not a gohard trace " +
			"(missing final newline)")
	}
	lines := strings.Split(string(data[:len(data)-1]), "\n")
	p := &parser{t: &Trace{}}
	if lines[0] != Magic {
		return nil, errors.New("line 1: not a gohard trace (want " +
			strconv.Quote(Magic) + ")")
	}
	heads := []string{"testbench", "end"}
	if len(lines) < 1+len(heads) {
		return nil, fmt.Errorf("line %d: missing header", len(lines)+1)
	}
	section := 0 // 0 types, 1 scopes, 2 signals, 3 processes, 4 entries
	for i, l := range lines[1:] {
		p.line = i + 2
		f, err := split(l)
		if err != nil {
			return nil, p.errf("%v", err)
		}
		if len(f) == 0 {
			return nil, p.errf("empty line")
		}
		p.f, p.k = f, 1
		if i == len(heads) && f[0] == "reproducible" {
			if len(f) != 2 || f[1] != "no" {
				return nil, p.errf("want reproducible no")
			}
			p.t.Header.NonReproducible = true
			continue
		}
		if i < len(heads) {
			if f[0] != heads[i] {
				return nil, p.errf("want %s", heads[i])
			}
			if err := p.header(i); err != nil {
				return nil, err
			}
			continue
		}
		var want int
		switch f[0] {
		case "type":
			want = 0
		case "scope":
			want = 1
		case "signal":
			want = 2
		case "process":
			want = 3
		default:
			want = 4
		}
		if want < section {
			return nil, p.errf("%s after a later section", f[0])
		}
		section = want
		if want == 4 {
			if p.t.types == nil {
				p.t.index() // the declarations are complete
			}
			p.k = 0
			err = p.entry()
		} else {
			err = p.decl(f[0])
		}
		if err != nil {
			return nil, err
		}
		if p.k != len(p.f) {
			return nil, p.errf("unexpected %s", p.f[p.k])
		}
	}
	return p.t, nil
}

func (p *parser) next() (string, error) {
	if p.k >= len(p.f) {
		return "", p.errf("unexpected end of line")
	}
	s := p.f[p.k]
	p.k++
	return s, nil
}

func (p *parser) more() bool { return p.k < len(p.f) }

func (p *parser) int(bits int) (int64, error) {
	s, err := p.next()
	if err != nil {
		return 0, err
	}
	n, err := strconv.ParseInt(s, 10, bits)
	if err != nil {
		return 0, p.errf("bad integer %s", s)
	}
	return n, nil
}

func (p *parser) uint(bits int) (uint64, error) {
	s, err := p.next()
	if err != nil {
		return 0, err
	}
	n, err := strconv.ParseUint(s, 10, bits)
	if err != nil {
		return 0, p.errf("bad number %s", s)
	}
	return n, nil
}

func (p *parser) header(i int) error {
	var err error
	switch i {
	case 0:
		p.t.Header.Testbench, err = p.next()
	case 1:
		var n uint64
		n, err = p.uint(64)
		p.t.Header.End = Time(n)
	}
	return err
}

func lookupName[T ~uint8](names []string, s string) (T, bool) {
	for i, n := range names {
		if n != "" && n == s {
			return T(i), true
		}
	}
	return 0, false
}

func (p *parser) decl(head string) error {
	switch head {
	case "type":
		return p.typ()
	case "scope":
		path, err := p.next()
		if err != nil {
			return err
		}
		ks, err := p.next()
		if err != nil {
			return err
		}
		k, ok := lookupName[ScopeKind](scopeNames[:], ks)
		if !ok {
			return p.errf("unknown scope kind %s", ks)
		}
		s := Scope{Path: path, Kind: k}
		if p.more() {
			s.Unit, _ = p.next()
		}
		p.t.Scopes = append(p.t.Scopes, s)
	case "signal":
		return p.signal()
	case "process":
		id, err := p.int(64)
		if err != nil {
			return err
		}
		if id != int64(len(p.t.Processes)+1) {
			return p.errf("process %d out of sequence", id)
		}
		path, err := p.next()
		if err != nil {
			return err
		}
		pr := Process{Path: path}
		if p.more() && p.f[p.k] == "modeled" {
			pr.Modeled = true
			p.k++
		}
		p.t.Processes = append(p.t.Processes, pr)
	}
	return nil
}

func (p *parser) typ() error {
	name, err := p.next()
	if err != nil {
		return err
	}
	ks, err := p.next()
	if err != nil {
		return err
	}
	k, ok := lookupName[Kind](kindNames[:], ks)
	if !ok {
		return p.errf("unknown type kind %s", ks)
	}
	ty := Type{Name: name, Kind: k}
	switch k {
	case Enum:
		for p.more() {
			l, _ := p.next()
			ty.Literals = append(ty.Literals, l)
		}
	case Integer, Physical:
		if ty.Low, err = p.int(64); err != nil {
			return err
		}
		if ty.High, err = p.int(64); err != nil {
			return err
		}
		for k == Physical && p.more() {
			u, _ := p.next()
			m, err := p.int(64)
			if err != nil {
				return err
			}
			ty.Units = append(ty.Units, Unit{Name: u, Mult: m})
		}
	case Array:
		if ty.Elem, err = p.next(); err != nil {
			return err
		}
	}
	p.t.Types = append(p.t.Types, ty)
	return nil
}

func (p *parser) signal() error {
	path, err := p.next()
	if err != nil {
		return err
	}
	ty, err := p.next()
	if err != nil {
		return err
	}
	ds, err := p.next()
	if err != nil {
		return err
	}
	d, ok := lookupName[Dir](dirNames[:], ds)
	if !ok {
		return p.errf("unknown mode %s", ds)
	}
	s := Signal{Path: path, Type: ty, Dir: d}
	if p.more() {
		if s.Left, err = p.int(64); err != nil {
			return err
		}
		to, err := p.next()
		if err != nil {
			return err
		}
		switch to {
		case "to":
		case "downto":
			s.Downto = true
		default:
			return p.errf("want to or downto, not %s", to)
		}
		if s.Right, err = p.int(64); err != nil {
			return err
		}
	}
	p.t.Signals = append(p.t.Signals, s)
	return nil
}

func (p *parser) entry() error {
	at, err := p.uint(64)
	if err != nil {
		return err
	}
	delta, err := p.uint(32)
	if err != nil {
		return err
	}
	what, err := p.next()
	if err != nil {
		return err
	}
	if what == "!" {
		return p.event(Time(at), uint32(delta))
	}
	i, ok := p.t.Lookup(what)
	if !ok {
		return p.errf("unknown signal %s", what)
	}
	c := Change{Time: Time(at), Delta: uint32(delta), Signal: i}
	ty, ok := p.t.Type(p.t.Signals[i].Type)
	if !ok {
		return p.errf("signal %s has unknown type", what)
	}
	if c.Value, err = p.value(ty); err != nil {
		return err
	}
	if c.Process, err = p.proc(); err != nil {
		return err
	}
	if p.more() && p.f[p.k] == "modeled" {
		c.Modeled = true
		p.k++
	}
	p.t.Changes = append(p.t.Changes, c)
	return nil
}

func (p *parser) proc() (int, error) {
	s, err := p.next()
	if err != nil {
		return 0, err
	}
	if s == "-" {
		return 0, nil
	}
	n, err := strconv.Atoi(strings.TrimPrefix(s, "p"))
	if err != nil || s[0] != 'p' || n < 1 {
		return 0, p.errf("bad process %s", s)
	}
	return n, nil
}

func (p *parser) value(ty *Type) (Value, error) {
	switch ty.Kind {
	case Logic:
		s, err := p.next()
		if err != nil {
			return Value{}, err
		}
		i := strings.Index(LogicChars, s)
		if len(s) != 1 || i < 0 {
			return Value{}, p.errf("bad std_ulogic value %s", s)
		}
		return Value{Int: int64(i)}, nil
	case Enum:
		s, err := p.next()
		if err != nil {
			return Value{}, err
		}
		for i, l := range ty.Literals {
			if l == s {
				return Value{Int: int64(i)}, nil
			}
		}
		return Value{}, p.errf("%s is not a literal of %s", s, ty.Name)
	case Integer, Physical:
		n, err := p.int(64)
		return Value{Int: n}, err
	case Real:
		s, err := p.next()
		if err != nil {
			return Value{}, err
		}
		f, err := strconv.ParseFloat(s, 64)
		if err != nil && !errors.Is(err, strconv.ErrRange) {
			return Value{}, p.errf("bad real %s", s)
		}
		return Value{Real: f}, nil
	}
	et, ok := p.t.Type(ty.Elem)
	if !ok || et.Kind == Array {
		return Value{}, p.errf("bad element type %s", ty.Elem)
	}
	if et.Kind == Logic {
		s, err := p.next()
		if err != nil {
			return Value{}, err
		}
		v := Value{Elems: make([]Value, len(s))}
		for i := range len(s) {
			k := strings.IndexByte(LogicChars, s[i])
			if k < 0 {
				return Value{}, p.errf("bad std_ulogic value %q", s[i])
			}
			v.Elems[i] = Value{Int: int64(k)}
		}
		return v, nil
	}
	if s, err := p.next(); err != nil || s != "[" {
		return Value{}, p.errf("want [ to start an array value")
	}
	v := Value{Elems: []Value{}}
	for {
		if !p.more() {
			return Value{}, p.errf("unterminated array value")
		}
		if p.f[p.k] == "]" {
			p.k++
			return v, nil
		}
		e, err := p.value(et)
		if err != nil {
			return Value{}, err
		}
		v.Elems = append(v.Elems, e)
	}
}

func (p *parser) event(at Time, delta uint32) error {
	e := Event{Time: at, Delta: delta}
	ks, err := p.next()
	if err != nil {
		return err
	}
	k, ok := lookupName[EventKind](eventNames[:], ks)
	if !ok {
		return p.errf("unknown event kind %s", ks)
	}
	e.Kind = k
	ss, err := p.next()
	if err != nil {
		return err
	}
	sev, ok := lookupName[Severity](severityNames[:], ss)
	if !ok {
		return p.errf("unknown severity %s", ss)
	}
	e.Severity = sev
	loc, err := p.next()
	if err != nil {
		return err
	}
	c := strings.LastIndexByte(loc, ':')
	if c < 0 {
		return p.errf("want FILE:LINE, not %s", loc)
	}
	e.File = loc[:c]
	if e.Line, err = strconv.Atoi(loc[c+1:]); err != nil {
		return p.errf("bad line in %s", loc)
	}
	if e.Process, err = p.proc(); err != nil {
		return err
	}
	msg, err := p.next()
	if err != nil {
		return err
	}
	e.Message = msg
	p.t.Events = append(p.t.Events, e)
	return nil
}

// split splits a line into tokens: runs of non-space bytes, or
// Go-quoted strings (which are unquoted). Tokens are separated by one
// space.
func split(l string) ([]string, error) {
	var f []string
	for l != "" {
		if l[0] == '"' {
			q, err := strconv.QuotedPrefix(l)
			if err != nil {
				return nil, errors.New("bad quoted string")
			}
			s, _ := strconv.Unquote(q)
			f = append(f, s)
			l = l[len(q):]
		} else {
			i := strings.IndexByte(l, ' ')
			if i < 0 {
				i = len(l)
			}
			f = append(f, l[:i])
			l = l[i:]
		}
		if l == "" {
			break
		}
		if l[0] != ' ' || len(l) == 1 {
			return nil, errors.New("tokens must be separated by " +
				"one space")
		}
		l = l[1:]
	}
	return f, nil
}
