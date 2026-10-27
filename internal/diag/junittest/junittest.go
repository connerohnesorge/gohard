// Package junittest validates JUnit XML against the committed copy of
// the Jenkins JUnit schema, junit-10.xsd (#61), for the tests of the
// JUnit renderer and of the testdata/cli fixtures.
//
// junit-10.xsd is from the Jenkins xUnit plugin
// (github.com/jenkinsci/xunit-plugin, commit ae25da50,
// src/main/resources/org/jenkinsci/plugins/xunit/types/model/xsd/),
// MIT licensed, unchanged. Go has no XML Schema validator, so this
// package interprets the subset of XML Schema the file uses: global
// elements, named and anonymous complex types (mixed or not, with
// sequence and choice particles and occurrence bounds, attributes with
// use="required"), and named simple types restricting xs:string by a
// pattern. Any other schema construct is an error, so a schema change
// can never pass by being ignored.
package junittest

import (
	"bytes"
	_ "embed"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"
)

//go:embed junit-10.xsd
var schemaXSD []byte

// Schema returns the bytes of the committed junit-10.xsd.
func Schema() []byte { return schemaXSD }

const xsNS = "http://www.w3.org/2001/XMLSchema"

// node is an XML element with its attributes, child elements and
// character data.
type node struct {
	name  string
	space string
	attrs []xml.Attr
	kids  []*node
	text  string
}

func (n *node) attr(name string) (string, bool) {
	for _, a := range n.attrs {
		if a.Name.Space == "" && a.Name.Local == name {
			return a.Value, true
		}
	}
	return "", false
}

// parse reads one XML document into a tree. Comments, processing
// instructions and the declaration are skipped.
func parse(data []byte) (*node, error) {
	dec := xml.NewDecoder(bytes.NewReader(data))
	dec.Strict = true
	var stack []*node
	var root *node
	for {
		tok, err := dec.Token()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			n := &node{name: t.Name.Local, space: t.Name.Space,
				attrs: t.Attr}
			if len(stack) == 0 {
				if root != nil {
					return nil, errors.New("more than one root element")
				}
				root = n
			} else {
				p := stack[len(stack)-1]
				p.kids = append(p.kids, n)
			}
			stack = append(stack, n)
		case xml.EndElement:
			stack = stack[:len(stack)-1]
		case xml.CharData:
			if len(stack) > 0 {
				stack[len(stack)-1].text += string(t)
			} else if strings.TrimSpace(string(t)) != "" {
				return nil, errors.New("text outside the root element")
			}
		}
	}
	if root == nil {
		return nil, errors.New("no root element")
	}
	return root, nil
}

// Schema model.

type attrDecl struct {
	name     string
	typ      string
	required bool
}

type complexType struct {
	mixed   bool
	content *particle // nil: no child elements
	attrs   []attrDecl
}

// elemDecl is a global or local element declaration: either a simple
// type (typ) or a complex type.
type elemDecl struct {
	name    string
	typ     string // a simple type name, when complex is nil
	complex *complexType
	// typeRef names a global complex type resolved lazily.
	typeRef string
}

type particleKind int

const (
	pElement particleKind = iota
	pSequence
	pChoice
)

type particle struct {
	kind     particleKind
	min, max int // max -1: unbounded
	elem     string
	local    *elemDecl // a local element declaration
	items    []*particle
}

type schema struct {
	elems    map[string]*elemDecl
	complex  map[string]*complexType
	patterns map[string]*regexp.Regexp
}

// load reads the schema subset; anything else is an error.
func load(data []byte) (*schema, error) {
	root, err := parse(data)
	if err != nil {
		return nil, err
	}
	if root.space != xsNS || root.name != "schema" {
		return nil, errors.New("xsd: root is not xs:schema")
	}
	s := &schema{elems: map[string]*elemDecl{},
		complex:  map[string]*complexType{},
		patterns: map[string]*regexp.Regexp{}}
	for _, k := range root.kids {
		if err := onlyAttrs(k, "name", "type", "mixed"); err != nil {
			return nil, err
		}
		name, _ := k.attr("name")
		switch xsName(k) {
		case "simpleType":
			re, err := simpleType(k)
			if err != nil {
				return nil, err
			}
			s.patterns[name] = re
		case "complexType":
			ct, err := s.complexType(k)
			if err != nil {
				return nil, err
			}
			s.complex[name] = ct
		case "element":
			e, err := s.element(k)
			if err != nil {
				return nil, err
			}
			s.elems[name] = e
		default:
			return nil, unsupported(k)
		}
	}
	return s, nil
}

func xsName(n *node) string {
	if n.space != xsNS {
		return "{" + n.space + "}" + n.name
	}
	return n.name
}

func unsupported(n *node) error {
	return fmt.Errorf("xsd: unsupported construct <%s>", xsName(n))
}

func onlyAttrs(n *node, names ...string) error {
	for _, a := range n.attrs {
		ok := a.Name.Space == "" && strings.Contains(" "+
			strings.Join(names, " ")+" ", " "+a.Name.Local+" ")
		if !ok {
			return fmt.Errorf("xsd: unsupported attribute %s on <%s>",
				a.Name.Local, xsName(n))
		}
	}
	return nil
}

func simpleType(n *node) (*regexp.Regexp, error) {
	if len(n.kids) != 1 || xsName(n.kids[0]) != "restriction" {
		return nil, unsupported(n)
	}
	r := n.kids[0]
	if base, _ := r.attr("base"); base != "xs:string" {
		return nil, fmt.Errorf("xsd: restriction of %q", base)
	}
	if len(r.kids) != 1 || xsName(r.kids[0]) != "pattern" {
		return nil, unsupported(r)
	}
	p, _ := r.kids[0].attr("value")
	return regexp.Compile(`^(?:` + p + `)$`)
}

func (s *schema) complexType(n *node) (*complexType, error) {
	if err := onlyAttrs(n, "name", "mixed"); err != nil {
		return nil, err
	}
	ct := &complexType{}
	if m, ok := n.attr("mixed"); ok {
		ct.mixed = m == "true"
	}
	for _, k := range n.kids {
		switch xsName(k) {
		case "sequence", "choice":
			if ct.content != nil {
				return nil, unsupported(k)
			}
			p, err := s.particle(k)
			if err != nil {
				return nil, err
			}
			ct.content = p
		case "attribute":
			if err := onlyAttrs(k, "name", "type", "use"); err != nil {
				return nil, err
			}
			a := attrDecl{}
			a.name, _ = k.attr("name")
			a.typ, _ = k.attr("type")
			use, _ := k.attr("use")
			switch use {
			case "", "optional":
			case "required":
				a.required = true
			default:
				return nil, fmt.Errorf("xsd: use=%q", use)
			}
			ct.attrs = append(ct.attrs, a)
		default:
			return nil, unsupported(k)
		}
	}
	return ct, nil
}

func (s *schema) element(n *node) (*elemDecl, error) {
	if err := onlyAttrs(n, "name", "type", "minOccurs",
		"maxOccurs"); err != nil {
		return nil, err
	}
	e := &elemDecl{}
	e.name, _ = n.attr("name")
	typ, hasType := n.attr("type")
	switch {
	case hasType && len(n.kids) == 0:
		if typ == "xs:string" {
			e.typ = typ
		} else {
			e.typeRef = typ
		}
	case !hasType && len(n.kids) == 1 &&
		xsName(n.kids[0]) == "complexType":
		ct, err := s.complexType(n.kids[0])
		if err != nil {
			return nil, err
		}
		e.complex = ct
	default:
		return nil, unsupported(n)
	}
	return e, nil
}

func occurs(n *node) (int, int, error) {
	lo, hi := 1, 1
	if v, ok := n.attr("minOccurs"); ok {
		x, err := strconv.Atoi(v)
		if err != nil {
			return 0, 0, err
		}
		lo = x
	}
	if v, ok := n.attr("maxOccurs"); ok {
		if v == "unbounded" {
			hi = -1
		} else {
			x, err := strconv.Atoi(v)
			if err != nil {
				return 0, 0, err
			}
			hi = x
		}
	}
	return lo, hi, nil
}

func (s *schema) particle(n *node) (*particle, error) {
	lo, hi, err := occurs(n)
	if err != nil {
		return nil, err
	}
	p := &particle{min: lo, max: hi}
	switch xsName(n) {
	case "element":
		p.kind = pElement
		if ref, ok := n.attr("ref"); ok {
			if err := onlyAttrs(n, "ref", "minOccurs",
				"maxOccurs"); err != nil {
				return nil, err
			}
			p.elem = ref
			return p, nil
		}
		e, err := s.element(n)
		if err != nil {
			return nil, err
		}
		p.elem, p.local = e.name, e
		return p, nil
	case "sequence", "choice":
		if err := onlyAttrs(n, "minOccurs", "maxOccurs"); err != nil {
			return nil, err
		}
		p.kind = pSequence
		if xsName(n) == "choice" {
			p.kind = pChoice
		}
		for _, k := range n.kids {
			q, err := s.particle(k)
			if err != nil {
				return nil, err
			}
			p.items = append(p.items, q)
		}
		return p, nil
	}
	return nil, unsupported(n)
}

// Validation.

// Validate checks that doc is well-formed XML valid against
// junit-10.xsd. The error names the first problem found.
func Validate(doc []byte) error {
	s, err := load(schemaXSD)
	if err != nil {
		return err
	}
	return s.validate(doc)
}

func (s *schema) validate(doc []byte) error {
	root, err := parse(doc)
	if err != nil {
		return err
	}
	if root.space != "" {
		return fmt.Errorf("root element in namespace %q", root.space)
	}
	e, ok := s.elems[root.name]
	if !ok {
		return fmt.Errorf("root element <%s> is not declared",
			root.name)
	}
	return s.check(root, e, "/"+root.name)
}

func (s *schema) check(n *node, e *elemDecl, at string) error {
	ct := e.complex
	if e.typeRef != "" {
		ct = s.complex[e.typeRef]
		if ct == nil {
			return fmt.Errorf("xsd: unknown type %q", e.typeRef)
		}
	}
	if ct == nil { // xs:string
		if len(n.attrs) > 0 {
			return fmt.Errorf("%s: attribute %s not allowed", at,
				n.attrs[0].Name.Local)
		}
		if len(n.kids) > 0 {
			return fmt.Errorf("%s: child <%s> not allowed", at,
				n.kids[0].name)
		}
		return nil
	}
	if err := s.checkAttrs(n, ct, at); err != nil {
		return err
	}
	if !ct.mixed && strings.TrimSpace(n.text) != "" {
		return fmt.Errorf("%s: text not allowed", at)
	}
	if ct.content == nil {
		if len(n.kids) > 0 {
			return fmt.Errorf("%s: child <%s> not allowed", at,
				n.kids[0].name)
		}
		return nil
	}
	ends, err := s.match(ct.content, n.kids, 0)
	if err != nil {
		return err
	}
	if !contains(ends, len(n.kids)) {
		return fmt.Errorf("%s: children %s do not match the content "+
			"model", at, names(n.kids))
	}
	for i, k := range n.kids {
		d, err := s.declFor(ct.content, k.name)
		if err != nil {
			return err
		}
		if k.space != "" {
			return fmt.Errorf("%s: <%s> in namespace %q", at, k.name,
				k.space)
		}
		path := at + "/" + k.name + "[" + strconv.Itoa(i) + "]"
		if err := s.check(k, d, path); err != nil {
			return err
		}
	}
	return nil
}

func (s *schema) checkAttrs(n *node, ct *complexType, at string) error {
	for _, a := range n.attrs {
		if a.Name.Space == "xmlns" || a.Name.Local == "xmlns" {
			continue
		}
		var d *attrDecl
		for i := range ct.attrs {
			if a.Name.Space == "" && ct.attrs[i].name == a.Name.Local {
				d = &ct.attrs[i]
			}
		}
		if d == nil {
			return fmt.Errorf("%s: attribute %s not allowed", at,
				a.Name.Local)
		}
		if d.typ != "xs:string" {
			re, ok := s.patterns[d.typ]
			if !ok {
				return fmt.Errorf("xsd: unknown type %q", d.typ)
			}
			if !re.MatchString(a.Value) {
				return fmt.Errorf("%s: attribute %s=%q is not a %s",
					at, d.name, a.Value, d.typ)
			}
		}
	}
	for _, d := range ct.attrs {
		if _, ok := n.attr(d.name); d.required && !ok {
			return fmt.Errorf("%s: required attribute %s missing", at,
				d.name)
		}
	}
	return nil
}

// declFor finds the declaration of child name in a content model.
func (s *schema) declFor(p *particle, name string) (*elemDecl, error) {
	if p.kind == pElement {
		if p.elem != name {
			return nil, nil
		}
		if p.local != nil {
			return p.local, nil
		}
		if e, ok := s.elems[p.elem]; ok {
			return e, nil
		}
		return nil, fmt.Errorf("xsd: unknown element %q", p.elem)
	}
	for _, q := range p.items {
		d, err := s.declFor(q, name)
		if d != nil || err != nil {
			return d, err
		}
	}
	return nil, fmt.Errorf("no declaration of <%s>", name)
}

// match returns every position the particle p, with its occurrence
// bounds, can end at when it starts at kids[i].
func (s *schema) match(p *particle, kids []*node, i int) ([]int, error) {
	var out []int
	var rep func(at, count int) error
	rep = func(at, count int) error {
		if count >= p.min {
			out = add(out, at)
		}
		if p.max >= 0 && count >= p.max {
			return nil
		}
		ends, err := s.once(p, kids, at)
		if err != nil {
			return err
		}
		for _, j := range ends {
			if j == at {
				// An empty match repeats forever: every remaining
				// minimum is met here.
				out = add(out, at)
				continue
			}
			if err := rep(j, count+1); err != nil {
				return err
			}
		}
		return nil
	}
	err := rep(i, 0)
	return out, err
}

// once returns the end positions of one occurrence of p at kids[i].
func (s *schema) once(p *particle, kids []*node, i int) ([]int, error) {
	switch p.kind {
	case pElement:
		if i < len(kids) && kids[i].name == p.elem {
			return []int{i + 1}, nil
		}
		return nil, nil
	case pChoice:
		var out []int
		for _, q := range p.items {
			ends, err := s.match(q, kids, i)
			if err != nil {
				return nil, err
			}
			for _, j := range ends {
				out = add(out, j)
			}
		}
		return out, nil
	}
	at := []int{i}
	for _, q := range p.items {
		var next []int
		for _, k := range at {
			ends, err := s.match(q, kids, k)
			if err != nil {
				return nil, err
			}
			for _, j := range ends {
				next = add(next, j)
			}
		}
		at = next
	}
	return at, nil
}

func add(xs []int, x int) []int {
	if contains(xs, x) {
		return xs
	}
	return append(xs, x)
}

func contains(xs []int, x int) bool {
	for _, y := range xs {
		if y == x {
			return true
		}
	}
	return false
}

func names(kids []*node) string {
	var b []string
	for _, k := range kids {
		b = append(b, "<"+k.name+">")
	}
	return "[" + strings.Join(b, " ") + "]"
}
