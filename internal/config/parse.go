package config

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"

	"go.yaml.in/yaml/v3"

	"github.com/connerohnesorge/gohard/v2/internal/diag"
	"github.com/connerohnesorge/gohard/v2/internal/span"
)

// Parse builds a Config from the bytes of a gohard.yaml. name is the
// path the diagnostics carry (relative to the project root, with '/').
// Empty input, or input holding only comments, gives Defaults with no
// diagnostic. Keys with problems keep their defaults; the diagnostics
// are sorted (diag.Normalize). Parse never panics.
//
// Editor settings sent as JSON go through Parse as well: JSON is YAML.
func Parse(name string, src []byte) (Config, []diag.Diagnostic) {
	p := &parser{name: name, src: src, idx: span.NewIndex(src),
		cfg: Defaults()}
	p.run(src)
	return p.cfg, diag.Normalize(p.diags)
}

type parser struct {
	name   string
	src    []byte
	idx    *span.Index
	cursor cursor
	cfg    Config
	diags  []diag.Diagnostic
}

func (p *parser) run(src []byte) {
	defer func() {
		// The YAML library is third-party code; a panic inside it must
		// not crash GoHard (P6).
		if v := recover(); v != nil {
			p.diags = append(p.diags, diag.New(diag.ConfigSyntax,
				fmt.Sprint(v)).At(p.name, 1, 1))
		}
	}()
	dec := yaml.NewDecoder(bytes.NewReader(src))
	var doc yaml.Node
	if err := dec.Decode(&doc); err != nil {
		if !errors.Is(err, io.EOF) {
			p.syntax(err)
		}
		return
	}
	var extra yaml.Node
	switch err := dec.Decode(&extra); {
	case err == nil:
		p.report(diag.New(diag.ConfigSyntax, FileName+
			" must hold a single YAML document"), &extra)
		return
	case !errors.Is(err, io.EOF):
		p.syntax(err)
		return
	}
	if len(doc.Content) == 0 {
		return
	}
	root := doc.Content[0]
	if isNull(root) {
		return
	}
	if root.Kind != yaml.MappingNode {
		p.report(diag.New(diag.ConfigType, FileName, "a mapping",
			describe(root)), root)
		return
	}
	p.group(root, "")
}

var lineRE = regexp.MustCompile(`^yaml: line (\d+): `)

// syntax reports a YAML syntax error. The library gives only a line, so
// the column is 1; the line is clamped to the input.
func (p *parser) syntax(err error) {
	msg, line := err.Error(), 1
	if m := lineRE.FindStringSubmatch(msg); m != nil {
		line, _ = strconv.Atoi(m[1])
		msg = msg[len(m[0]):]
	}
	msg = strings.TrimPrefix(msg, "yaml: ")
	line = max(1, min(line, p.idx.LineCount()))
	p.diags = append(p.diags,
		diag.New(diag.ConfigSyntax, msg).At(p.name, line, 1))
}

// pos converts a node position (one-based line, one-based column counted
// in characters) to a one-based UTF-8 byte column, clamped so it always
// names a real place in the input. Nodes are mostly visited in document
// order, so pos resumes from the previous position on the same line and
// a long single-line file costs linear time, not quadratic.
func (p *parser) pos(n *yaml.Node) Pos {
	line := min(max(1, n.Line), p.idx.LineCount())
	col := max(1, n.Column)
	c := &p.cursor
	if c.line != line || c.col > col {
		off := p.idx.LineStart(line - 1)
		if line == 1 && bytes.HasPrefix(p.src, utf8BOM) {
			off += len(utf8BOM) // yaml.v3 does not count the BOM
		}
		*c = cursor{line: line, col: 1, off: off}
	}
	end := p.idx.LineEnd(line - 1)
	for c.col < col && c.off < end {
		_, size := utf8.DecodeRune(p.src[c.off:end])
		c.off += size
		c.col++
	}
	c.col = col // a column past the line end stays clamped
	return Pos{Line: line, Col: c.off - p.idx.LineStart(line-1) + 1}
}

var utf8BOM = []byte("\ufeff")

// cursor is the last position pos converted.
type cursor struct {
	line, col, off int
}

func (p *parser) report(d diag.Diagnostic, n *yaml.Node) {
	p.reportAt(d, p.pos(n))
}

func (p *parser) reportAt(d diag.Diagnostic, at Pos) {
	p.diags = append(p.diags, d.At(p.name, at.Line, at.Col))
}

// group decodes a mapping of keys whose dotted names start with prefix.
func (p *parser) group(m *yaml.Node, prefix string) {
	seen := map[string]Pos{}
	for i := 0; i+1 < len(m.Content); i += 2 {
		k, v := m.Content[i], m.Content[i+1]
		name, ok := p.keyName(k, prefix)
		if !ok {
			continue
		}
		full := join(prefix, name)
		kp := p.pos(k)
		if first, dup := seen[name]; dup {
			p.reportAt(diag.New(diag.ConfigDuplicateKey, full,
				strconv.Itoa(first.Line)), kp)
			continue
		}
		seen[name] = kp
		if key, ok := lookupKey(full); ok {
			if key.acc.decode(p, full, v, &p.cfg) {
				p.cfg.mark(full, kp)
				if key.acc.isDefault(&p.cfg) {
					p.report(diag.New(diag.ConfigRedundantDefault,
						full, key.Default), v)
				}
			}
			continue
		}
		if !isGroup(full) {
			p.reportAt(diag.New(diag.ConfigUnknownKey, full), kp)
			continue
		}
		switch {
		case v.Kind == yaml.MappingNode:
			p.group(v, full)
		case isNull(v):
			// "format:" with every child commented out.
		default:
			p.report(diag.New(diag.ConfigType, full, "a mapping",
				describe(v)), v)
		}
	}
}

// keyName returns the text of a mapping key, which must be a string
// scalar.
func (p *parser) keyName(k *yaml.Node, prefix string) (string, bool) {
	if k.Kind != yaml.ScalarNode || isNull(k) || badTag(k) {
		where := "a key"
		if prefix != "" {
			where = "a key under " + prefix
		}
		p.report(diag.New(diag.ConfigType, where, "a string",
			describe(k)), k)
		return "", false
	}
	return k.Value, true
}

func join(prefix, name string) string {
	if prefix == "" {
		return name
	}
	return prefix + "." + name
}

// quoted reports whether a scalar is written as a string whatever its
// text: quoted, a block scalar, or tagged !!str.
func quoted(n *yaml.Node) bool {
	const strStyles = yaml.DoubleQuotedStyle | yaml.SingleQuotedStyle |
		yaml.LiteralStyle | yaml.FoldedStyle
	return n.Style&strStyles != 0 ||
		n.Style&yaml.TaggedStyle != 0 && n.Tag == "!!str"
}

// badTag reports an explicit tag other than !!str, which GoHard does
// not interpret.
func badTag(n *yaml.Node) bool {
	return n.Style&yaml.TaggedStyle != 0 && n.Tag != "!!str"
}

func isNull(n *yaml.Node) bool {
	return n.Kind == yaml.ScalarNode && !quoted(n) && n.Tag == "!!null"
}

// describe names what a node is, for "must be X, not Y" messages.
func describe(n *yaml.Node) string {
	switch n.Kind {
	case yaml.MappingNode:
		return "a mapping"
	case yaml.SequenceNode:
		return "a list"
	case yaml.AliasNode:
		return "an alias (*" + n.Value + ")"
	case yaml.DocumentNode:
		return "a document"
	}
	switch {
	case badTag(n):
		return "a value tagged " + n.Tag
	case isNull(n):
		return "null"
	case quoted(n):
		return "the string " + strconv.Quote(n.Value)
	}
	switch n.Tag {
	case "!!int", "!!float":
		return "the number " + display(n)
	case "!!bool":
		return "the boolean " + display(n)
	}
	return "the string " + display(n)
}

// display shows a scalar the way the user wrote it: quoted values in
// quotes, plain values bare (quoted when they hold unusual characters).
func display(n *yaml.Node) string {
	if quoted(n) || !strconv.CanBackquote(n.Value) {
		return strconv.Quote(n.Value)
	}
	return n.Value
}

// scalar checks that n is a non-null, untagged scalar; otherwise it
// reports a type error saying the key must be want.
func (p *parser) scalar(key, want string, n *yaml.Node) bool {
	if n.Kind == yaml.ScalarNode && !isNull(n) && !badTag(n) {
		return true
	}
	p.report(diag.New(diag.ConfigType, key, want, describe(n)), n)
	return false
}

// str decodes a free-form string. An unquoted YAML 1.1 boolean spelling
// is an error that tells the user to quote it.
func (p *parser) str(key string, n *yaml.Node) (string, bool) {
	if !p.scalar(key, "a string", n) {
		return "", false
	}
	if !quoted(n) && isYAML11Bool(n.Value) {
		p.report(diag.New(diag.ConfigUnquotedBool, key, n.Value), n)
		return "", false
	}
	return n.Value, true
}

// checked decodes a string and checks it with ok, reporting a type error
// saying the key must be want.
func (p *parser) checked(key, want string, n *yaml.Node,
	ok func(string) bool,
) (string, bool) {
	s, good := p.str(key, n)
	if !good {
		return "", false
	}
	if !ok(s) {
		p.report(diag.New(diag.ConfigType, key, want,
			describe(n)), n)
		return "", false
	}
	return s, true
}

func (p *parser) boolean(key string, n *yaml.Node) (bool, bool) {
	if !p.scalar(key, "true or false", n) {
		return false, false
	}
	if !quoted(n) {
		switch n.Value {
		case "true":
			return true, true
		case "false":
			return false, true
		}
	}
	p.report(diag.New(diag.ConfigBadBool, key, display(n)), n)
	return false, false
}

var decimalRE = regexp.MustCompile(`^-?(0|[1-9][0-9]*)$`)

// integer decodes a decimal integer in [lo, hi]; bound describes the
// range for the message.
func (p *parser) integer(key string, n *yaml.Node, lo, hi uint64,
	bound string,
) (uint64, bool) {
	const want = "a decimal integer"
	if !p.scalar(key, want, n) {
		return 0, false
	}
	if quoted(n) || !decimalRE.MatchString(n.Value) {
		p.report(diag.New(diag.ConfigType, key, want, describe(n)), n)
		return 0, false
	}
	v, err := strconv.ParseUint(n.Value, 10, 64)
	neg := strings.HasPrefix(n.Value, "-")
	if neg {
		// No key accepts a negative number.
		err = strconv.ErrRange
	}
	if err != nil || v < lo || v > hi {
		if !neg && (err != nil || v > hi) {
			// Too large: "at least lo" would be false.
			bound = "between " + strconv.FormatUint(lo, 10) + " and " +
				strconv.FormatUint(hi, 10)
		}
		p.report(diag.New(diag.ConfigRange, key, bound, n.Value), n)
		return 0, false
	}
	return v, true
}

func (p *parser) enum(key string, allowed []string,
	n *yaml.Node,
) (string, bool) {
	if !p.scalar(key, "a string", n) {
		return "", false
	}
	ok := false
	for _, a := range allowed {
		ok = ok || n.Value == a
	}
	if key == "standard" && !quoted(n) {
		if ok {
			p.report(diag.New(diag.ConfigUnquotedStandard, n.Value), n)
			return "", false
		}
	}
	if !ok {
		p.report(diag.New(diag.ConfigEnum, key, orList(allowed),
			display(n)), n)
		return "", false
	}
	return n.Value, true
}

// orList renders "a", "b" or "c" with every value quoted.
func orList(vs []string) string {
	q := make([]string, len(vs))
	for i, v := range vs {
		q[i] = strconv.Quote(v)
	}
	if len(q) == 1 {
		return q[0]
	}
	return strings.Join(q[:len(q)-1], ", ") + " or " + q[len(q)-1]
}

func (p *parser) vhdlTime(key string, n *yaml.Node) (Time, bool) {
	if !p.scalar(key, "a VHDL time string", n) {
		return 0, false
	}
	t, e := parseTime(n.Value)
	switch e {
	case timeMalformed:
		p.report(diag.New(diag.ConfigBadTime, key, display(n)), n)
		return 0, false
	case timeOverflow:
		p.report(diag.New(diag.ConfigRange, key,
			"at most "+Time(1<<63-1).String(), display(n)), n)
		return 0, false
	}
	return t, true
}

// list decodes a sequence, calling item for each element with its key
// path key[i].
func (p *parser) list(key string, n *yaml.Node,
	item func(key string, n *yaml.Node) bool,
) bool {
	if n.Kind != yaml.SequenceNode {
		p.report(diag.New(diag.ConfigType, key, "a list",
			describe(n)), n)
		return false
	}
	ok := true
	for i, e := range n.Content {
		ok = item(key+"["+strconv.Itoa(i)+"]", e) && ok
	}
	return ok
}

// entries decodes a user-keyed mapping. name checks each key; fold makes
// duplicate detection case-insensitive (VHDL identifiers). entry gets
// the key text, the key node and the value node.
func (p *parser) entries(key string, n *yaml.Node, want string,
	name func(string) bool, fold bool,
	entry func(k string, kp Pos, vn *yaml.Node) bool,
) bool {
	if n.Kind != yaml.MappingNode {
		p.report(diag.New(diag.ConfigType, key, "a mapping",
			describe(n)), n)
		return false
	}
	ok := true
	seen := map[string]Pos{}
	for i := 0; i+1 < len(n.Content); i += 2 {
		kn, vn := n.Content[i], n.Content[i+1]
		k, good := p.keyName(kn, key)
		if !good {
			ok = false
			continue
		}
		full := key + "." + k
		kp := p.pos(kn)
		if !name(k) {
			p.reportAt(diag.New(diag.ConfigType, "a key under "+key,
				want, describe(kn)), kp)
			ok = false
			continue
		}
		id := k
		if fold {
			id = strings.ToLower(k)
		}
		if first, dup := seen[id]; dup {
			p.reportAt(diag.New(diag.ConfigDuplicateKey, full,
				strconv.Itoa(first.Line)), kp)
			ok = false
			continue
		}
		seen[id] = kp
		ok = entry(k, kp, vn) && ok
	}
	return ok
}
