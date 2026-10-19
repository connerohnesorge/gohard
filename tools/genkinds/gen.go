package main

import (
	"bytes"
	"fmt"
	"go/format"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"go.yaml.in/yaml/v3"
)

// File is one generated file: a slash path from the repository root and
// its content.
type File struct {
	Path string
	Data []byte
}

// Inputs.
const (
	kindsPath    = "internal/cst/kinds.yaml"
	keywordsPath = "internal/token/keywords.yaml"
)

// firstKeyword is the token kind number of the first keyword; ordinary
// tokens are numbered from 1 and must stay below it.
const firstKeyword = 256

var standards = []string{"1993", "2002", "2008", "2019"}

type tokenDef struct {
	Name   string `yaml:"name"`
	Text   string `yaml:"text"`
	Trivia bool   `yaml:"trivia"`
	PSL    bool   `yaml:"psl"`
	Std    string `yaml:"std"`
	Doc    string `yaml:"doc"`
}

type fieldDef struct {
	Name   string   `yaml:"name"`
	Token  string   `yaml:"token"`
	Tokens []string `yaml:"tokens"`
	Node   string   `yaml:"node"`
	Nth    int      `yaml:"nth"`
	List   bool     `yaml:"list"`
	// Last selects the last matching child node instead of the nth
	// (node fields only): `actual` of [formal =>] actual.
	Last bool `yaml:"last"`
	// With names a token kind: the field is present only when the node
	// has a child token of that kind (`formal` only with `=>`).
	With string `yaml:"with"`
}

type nodeDef struct {
	Name   string     `yaml:"name"`
	Std    string     `yaml:"std"`
	Doc    string     `yaml:"doc"`
	Fields []fieldDef `yaml:"fields"`
}

type groupDef struct {
	Name  string   `yaml:"name"`
	Kinds []string `yaml:"kinds"`
}

type kindsFile struct {
	Schema int        `yaml:"schema"`
	Tokens []tokenDef `yaml:"tokens"`
	Nodes  []nodeDef  `yaml:"nodes"`
	Groups []groupDef `yaml:"groups"`
}

type wordDef struct {
	Word     string   `yaml:"word"`
	Class    string   `yaml:"class"`
	First    string   `yaml:"first"`
	Removed  string   `yaml:"removed"`
	PSL      bool     `yaml:"psl"`
	Suffixes []string `yaml:"suffixes"`
	Context  string   `yaml:"context"`
	LCS      string   `yaml:"lcs"`
}

type stdDef struct {
	Name     string `yaml:"name"`
	Inherits string `yaml:"inherits"`
	Add      int    `yaml:"add"`
	Verified *bool  `yaml:"verified_against_lrm"`
}

type keywordsFile struct {
	Schema    int       `yaml:"schema"`
	Standards []stdDef  `yaml:"standards"`
	Words     []wordDef `yaml:"words"`
}

// model is the validated input.
type model struct {
	kinds kindsFile
	words []wordDef
	// tokenKinds maps every token kind name (incl. keywords) to its Go
	// constant expression in package token.
	tokenKinds map[string]bool
	nodeKinds  map[string]bool
	groups     map[string]groupDef
}

// Generate reads the inputs under root and returns every generated file
// in a fixed order.
func Generate(root string) ([]File, error) {
	m, err := load(root)
	if err != nil {
		return nil, err
	}
	gens := []struct {
		path string
		fn   func(*model) string
	}{
		{"internal/token/kind_gen.go", genTokenKinds},
		{"internal/token/keywords_gen.go", genKeywords},
		{"internal/lexer/keywords_gen_test.go", genKeywordEdges},
		{"internal/cst/kind_gen.go", genNodeKinds},
		{"internal/cst/wrap_gen.go", genWrappers},
		{"internal/parser/edits_gen_test.go", genEdits},
	}
	var out []File
	for _, g := range gens {
		src := g.fn(m)
		data, err := format.Source([]byte(src))
		if err != nil {
			return nil, fmt.Errorf("%s: %v", g.path, err)
		}
		out = append(out, File{Path: g.path, Data: data})
	}
	return out, nil
}

func readYAML(root, path string, v any) error {
	data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(path)))
	if err != nil {
		return err
	}
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(v); err != nil {
		return fmt.Errorf("%s: %v", path, err)
	}
	return nil
}

func load(root string) (*model, error) {
	m := &model{
		tokenKinds: map[string]bool{},
		nodeKinds:  map[string]bool{},
		groups:     map[string]groupDef{},
	}
	if err := readYAML(root, kindsPath, &m.kinds); err != nil {
		return nil, err
	}
	var kw keywordsFile
	if err := readYAML(root, keywordsPath, &kw); err != nil {
		return nil, err
	}
	m.words = kw.Words
	if m.kinds.Schema != 1 || kw.Schema != 1 {
		return nil, fmt.Errorf("unsupported schema")
	}
	if len(m.kinds.Tokens) >= firstKeyword {
		return nil, fmt.Errorf("too many tokens (max %d)", firstKeyword-1)
	}
	for _, t := range m.kinds.Tokens {
		if !isGoName(t.Name) || m.tokenKinds[t.Name] {
			return nil, fmt.Errorf("token %q: bad or duplicate name",
				t.Name)
		}
		if t.Std != "" && !slices.Contains(standards, t.Std) {
			return nil, fmt.Errorf("token %s: bad std %q", t.Name, t.Std)
		}
		m.tokenKinds[t.Name] = true
	}
	if err := checkKeywords(&kw); err != nil {
		return nil, err
	}
	for _, w := range m.words {
		name := keywordName(w.Word)
		if m.tokenKinds[name] {
			return nil, fmt.Errorf("keyword %q: duplicate kind %s",
				w.Word, name)
		}
		m.tokenKinds[name] = true
	}
	for _, n := range m.kinds.Nodes {
		if !isGoName(n.Name) || m.nodeKinds[n.Name] {
			return nil, fmt.Errorf("node %q: bad or duplicate name",
				n.Name)
		}
		if n.Std != "" && !slices.Contains(standards, n.Std) {
			return nil, fmt.Errorf("node %s: bad std %q", n.Name, n.Std)
		}
		m.nodeKinds[n.Name] = true
	}
	for _, g := range m.kinds.Groups {
		if m.nodeKinds[g.Name] || m.groups[g.Name].Name != "" {
			return nil, fmt.Errorf("group %q: name clash", g.Name)
		}
		for _, k := range g.Kinds {
			if !m.nodeKinds[k] {
				return nil, fmt.Errorf("group %s: unknown kind %s",
					g.Name, k)
			}
		}
		m.groups[g.Name] = g
	}
	for _, n := range m.kinds.Nodes {
		seen := map[string]bool{}
		for _, f := range n.Fields {
			if err := m.checkField(n, f, seen); err != nil {
				return nil, err
			}
		}
	}
	return m, nil
}

// reservedMethods are the methods of cst.Node that a generated field
// accessor must not shadow.
var reservedMethods = []string{
	"Accepts", "Ancestors", "Child", "Children", "Elements", "Equal",
	"FirstToken", "FullSpan", "Green", "ID", "Inactive", "IsZero", "Kind",
	"LastToken", "NumChildren", "Nodes", "Offset", "Parent", "Span",
	"String", "Text", "Token", "TokenIn", "Tokens", "TokensIn", "Element",
	"Index", "Descendants", "TokenAt", "Covering", "Root", "LastChild",
}

func (m *model) checkField(n nodeDef, f fieldDef, seen map[string]bool,
) error {
	meth := camel(f.Name)
	if f.Name == "" || seen[f.Name] || slices.Contains(reservedMethods,
		meth) {
		return fmt.Errorf("node %s: bad field name %q", n.Name, f.Name)
	}
	seen[f.Name] = true
	set := 0
	if f.Token != "" {
		set++
		if !m.tokenKinds[f.Token] {
			return fmt.Errorf("node %s.%s: unknown token %s", n.Name,
				f.Name, f.Token)
		}
	}
	if len(f.Tokens) > 0 {
		set++
		for _, t := range f.Tokens {
			if !m.tokenKinds[t] {
				return fmt.Errorf("node %s.%s: unknown token %s",
					n.Name, f.Name, t)
			}
		}
	}
	if f.Node != "" {
		set++
		if !m.nodeKinds[f.Node] && m.groups[f.Node].Name == "" {
			return fmt.Errorf("node %s.%s: unknown node %s", n.Name,
				f.Name, f.Node)
		}
	}
	if set != 1 {
		return fmt.Errorf("node %s.%s: exactly one of token, tokens, "+
			"node", n.Name, f.Name)
	}
	if f.Nth < 0 || (f.List && f.Nth != 0) {
		return fmt.Errorf("node %s.%s: bad nth", n.Name, f.Name)
	}
	if f.Last && (f.Node == "" || f.List || f.Nth != 0) {
		return fmt.Errorf("node %s.%s: last needs a single node field "+
			"without nth", n.Name, f.Name)
	}
	if f.With != "" && (f.List || !m.tokenKinds[f.With]) {
		return fmt.Errorf("node %s.%s: bad with %q", n.Name, f.Name,
			f.With)
	}
	return nil
}

func checkKeywords(kw *keywordsFile) error {
	seen := map[string]bool{}
	adds := map[string]int{}
	for _, w := range kw.Words {
		if w.Word == "" || w.Word != strings.ToLower(w.Word) ||
			seen[w.Word] {
			return fmt.Errorf("keyword %q: bad or duplicate", w.Word)
		}
		seen[w.Word] = true
		switch w.Class {
		case "vhdl", "psl2008", "psl", "never":
		default:
			return fmt.Errorf("keyword %s: bad class %q", w.Word, w.Class)
		}
		if !slices.Contains(standards, w.First) {
			return fmt.Errorf("keyword %s: bad first %q", w.Word, w.First)
		}
		if w.Removed != "" && (!slices.Contains(standards, w.Removed) ||
			w.Removed <= w.First) {
			return fmt.Errorf("keyword %s: bad removed %q", w.Word,
				w.Removed)
		}
		for _, s := range w.Suffixes {
			if s != "!" && s != "_" && s != "!_" {
				return fmt.Errorf("keyword %s: bad suffix %q", w.Word, s)
			}
		}
		if w.Class == "vhdl" || w.Class == "psl2008" {
			adds[w.First]++
		}
	}
	if len(kw.Standards) != len(standards) {
		return fmt.Errorf("keywords: want standards %v", standards)
	}
	for i, s := range kw.Standards {
		if s.Name != standards[i] {
			return fmt.Errorf("keywords: standard %d is %q, want %q", i,
				s.Name, standards[i])
		}
		want := ""
		if i > 0 {
			want = standards[i-1]
		}
		if s.Inherits != want {
			return fmt.Errorf("keywords: %s must inherit %q", s.Name,
				want)
		}
		if adds[s.Name] != s.Add {
			return fmt.Errorf("keywords: %s adds %d reserved words, "+
				"table says %d", s.Name, adds[s.Name], s.Add)
		}
	}
	return nil
}

func isGoName(s string) bool {
	if s == "" || s[0] < 'A' || s[0] > 'Z' {
		return false
	}
	for _, c := range s {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' ||
			c >= '0' && c <= '9') {
			return false
		}
	}
	return true
}

// camel turns snake_case into CamelCase.
func camel(s string) string {
	var b strings.Builder
	up := true
	for _, c := range s {
		if c == '_' {
			up = true
			continue
		}
		if up && c >= 'a' && c <= 'z' {
			c -= 'a' - 'A'
		}
		up = false
		b.WriteRune(c)
	}
	return b.String()
}

func keywordName(word string) string { return "Kw" + camel(word) }

func stdConst(s string) string {
	if s == "" {
		s = "1993"
	}
	return "Std" + s
}

const header = "// Code generated by tools/genkinds. DO NOT EDIT.\n" +
	"// Sources: internal/cst/kinds.yaml, internal/token/keywords.yaml.\n\n"

func q(s string) string { return fmt.Sprintf("%q", s) }

func genTokenKinds(m *model) string {
	var b strings.Builder
	b.WriteString(header + "package token\n\n")
	b.WriteString("// Token kinds (internal/cst/kinds.yaml, append only).\n")
	b.WriteString("const (\n")
	for i, t := range m.kinds.Tokens {
		doc := t.Doc
		if doc == "" && t.Text != "" {
			doc = q(t.Text)
		}
		if doc != "" {
			fmt.Fprintf(&b, "\t// %s is %s.\n", t.Name, doc)
		}
		fmt.Fprintf(&b, "\t%s Kind = %d\n", t.Name, i+1)
	}
	b.WriteString(")\n\n")
	b.WriteString("// Keyword kinds (internal/token/keywords.yaml, " +
		"append only).\nconst (\n")
	for i, w := range m.words {
		fmt.Fprintf(&b, "\t%s Kind = %d // %s\n", keywordName(w.Word),
			firstKeyword+i, w.Word)
	}
	b.WriteString(")\n\n")
	fmt.Fprintf(&b, "// numTokens is one past the last ordinary token "+
		"kind.\nconst numTokens = %d\n\n", len(m.kinds.Tokens)+1)
	fmt.Fprintf(&b, "// numKeywords is the number of keyword kinds.\n"+
		"const numKeywords = %d\n\n", len(m.words))
	b.WriteString("var tokenInfo = [numTokens]info{\n")
	for _, t := range m.kinds.Tokens {
		var flags []string
		if t.Trivia {
			flags = append(flags, "flagTrivia")
		}
		if t.PSL {
			flags = append(flags, "flagPSL")
		}
		fl := "0"
		if len(flags) > 0 {
			fl = strings.Join(flags, "|")
		}
		fmt.Fprintf(&b, "\t%s: {name: %s, text: %s, std: %s, flags: %s},\n",
			t.Name, q(t.Name), q(t.Text), stdConst(t.Std), fl)
	}
	b.WriteString("}\n")
	return b.String()
}

func classConst(c string) string {
	switch c {
	case "vhdl":
		return "ClassVHDL"
	case "psl2008":
		return "ClassPSL2008"
	case "psl":
		return "ClassPSL"
	}
	return "ClassNever"
}

func suffixConst(ss []string) string {
	var out []string
	for _, s := range ss {
		switch s {
		case "!":
			out = append(out, "SuffixBang")
		case "_":
			out = append(out, "SuffixUnderscore")
		case "!_":
			out = append(out, "SuffixBangUnderscore")
		}
	}
	if len(out) == 0 {
		return "0"
	}
	return strings.Join(out, "|")
}

func genKeywords(m *model) string {
	var b strings.Builder
	b.WriteString(header + "package token\n\n")
	b.WriteString("var keywordTable = [numKeywords]Keyword{\n")
	for _, w := range m.words {
		removed := "0"
		if w.Removed != "" {
			removed = stdConst(w.Removed)
		}
		fmt.Fprintf(&b, "\t{Word: %s, Kind: %s, Class: %s, First: %s, "+
			"Removed: %s, PSL: %v, Suffixes: %s, Context: %s},\n",
			q(w.Word), keywordName(w.Word), classConst(w.Class),
			stdConst(w.First), removed, w.PSL, suffixConst(w.Suffixes),
			q(w.Context))
	}
	b.WriteString("}\n\n")
	b.WriteString("// keywordByWord maps each lower-case word to its " +
		"kind.\nvar keywordByWord = map[string]Kind{\n")
	for _, w := range m.words {
		fmt.Fprintf(&b, "\t%s: %s,\n", q(w.Word), keywordName(w.Word))
	}
	b.WriteString("}\n")
	return b.String()
}

// expectReserved is the generator's own statement of the reservation
// rules (#6, #11 entry 3); the lexer's implementation is checked against
// it through the generated edge values.
func expectReserved(w wordDef, std string, psl, strict bool) bool {
	switch w.Class {
	case "vhdl":
		in := std >= w.First && (w.Removed == "" || std < w.Removed)
		return in || (psl && w.PSL)
	case "psl2008":
		return psl || (strict && std >= "2008")
	case "psl":
		return psl
	}
	return false
}

func genKeywordEdges(m *model) string {
	var b strings.Builder
	b.WriteString(header + "package lexer\n\n")
	b.WriteString("import \"github.com/connerohnesorge/gohard/" +
		"internal/token\"\n\n")
	b.WriteString("// keywordEdges lists every keyword under every " +
		"standard, lexer mode\n// and psl.strict_keywords setting with " +
		"the kind the lexer must give.\nvar keywordEdges = " +
		"[]keywordEdge{\n")
	for _, w := range m.words {
		for _, std := range standards {
			for _, psl := range []bool{false, true} {
				for _, strict := range []bool{false, true} {
					kind := "token.Identifier"
					if expectReserved(w, std, psl, strict) {
						kind = "token." + keywordName(w.Word)
					}
					fmt.Fprintf(&b, "\t{%s, token.Std%s, %v, %v, %s},\n",
						q(w.Word), std, psl, strict, kind)
				}
			}
		}
	}
	b.WriteString("}\n")
	return b.String()
}

func genNodeKinds(m *model) string {
	var b strings.Builder
	b.WriteString(header + "package cst\n\n")
	b.WriteString("import \"github.com/connerohnesorge/gohard/" +
		"internal/token\"\n\n")
	b.WriteString("// Node kinds (kinds.yaml, append only).\nconst (\n")
	for i, n := range m.kinds.Nodes {
		if n.Doc != "" {
			fmt.Fprintf(&b, "\t// Kind%s: %s.\n", n.Name, n.Doc)
		}
		fmt.Fprintf(&b, "\tKind%s Kind = %d\n", n.Name, i+1)
	}
	b.WriteString(")\n\n")
	fmt.Fprintf(&b, "// numKinds is one past the last node kind.\n"+
		"const numKinds = %d\n\n", len(m.kinds.Nodes)+1)
	b.WriteString("var kindInfo = [numKinds]kindData{\n")
	for _, n := range m.kinds.Nodes {
		fmt.Fprintf(&b, "\tKind%s: {name: %s, std: token.%s},\n", n.Name,
			q(n.Name), stdConst(n.Std))
	}
	b.WriteString("}\n\n")
	b.WriteString("// groupNames lists the kind groups in kinds.yaml " +
		"order.\nvar groupNames = []string{\n")
	for _, g := range m.kinds.Groups {
		fmt.Fprintf(&b, "\t%s,\n", q(g.Name))
	}
	b.WriteString("}\n\n")
	b.WriteString("// groupKinds lists the members of each group.\n" +
		"var groupKinds = [][]Kind{\n")
	for _, g := range m.kinds.Groups {
		b.WriteString("\t{")
		for i, k := range g.Kinds {
			if i > 0 {
				b.WriteString(", ")
			}
			b.WriteString("Kind" + k)
		}
		b.WriteString("},\n")
	}
	b.WriteString("}\n")
	return b.String()
}

func (m *model) fieldType(f fieldDef) string {
	switch {
	case f.Node != "":
		return f.Node
	default:
		return "Token"
	}
}

func genWrappers(m *model) string {
	var b strings.Builder
	b.WriteString(header + "package cst\n\n")
	b.WriteString("import (\n\t\"iter\"\n\n\t\"github.com/connerohnesorge/" +
		"gohard/internal/token\"\n)\n\n")
	b.WriteString(genericMethods)
	// Token sets for multi-kind token fields, in a fixed order.
	type setVar struct{ name, kinds string }
	var sets []setVar
	for _, n := range m.kinds.Nodes {
		for _, f := range n.Fields {
			if len(f.Tokens) == 0 {
				continue
			}
			ks := make([]string, len(f.Tokens))
			for i, t := range f.Tokens {
				ks[i] = "token." + t
			}
			sets = append(sets, setVar{
				name:  "set" + n.Name + camel(f.Name),
				kinds: strings.Join(ks, ", "),
			})
		}
	}
	if len(sets) > 0 {
		b.WriteString("var (\n")
		for _, s := range sets {
			fmt.Fprintf(&b, "\t%s = token.NewSet(%s)\n", s.name, s.kinds)
		}
		b.WriteString(")\n\n")
	}
	for _, n := range m.kinds.Nodes {
		fmt.Fprintf(&b, "// %s is a typed view of a Kind%s node.\n",
			n.Name, n.Name)
		fmt.Fprintf(&b, "type %s struct{ Node }\n\n", n.Name)
		fmt.Fprintf(&b, "// Accepts reports whether k is Kind%s.\n",
			n.Name)
		fmt.Fprintf(&b, "func (%s) Accepts(k Kind) bool { return k == "+
			"Kind%s }\n\n", n.Name, n.Name)
		for _, f := range n.Fields {
			writeField(&b, m, n, f)
		}
	}
	for _, g := range m.kinds.Groups {
		fmt.Fprintf(&b, "// %s is a typed view of any of these kinds: "+
			"%s.\n", g.Name, strings.Join(g.Kinds, ", "))
		fmt.Fprintf(&b, "type %s struct{ Node }\n\n", g.Name)
		fmt.Fprintf(&b, "// Accepts reports whether k belongs to the "+
			"%s group.\n", g.Name)
		fmt.Fprintf(&b, "func (%s) Accepts(k Kind) bool {\n\tswitch k "+
			"{\n\tcase ", g.Name)
		for i, k := range g.Kinds {
			if i > 0 {
				b.WriteString(", ")
			}
			b.WriteString("Kind" + k)
		}
		b.WriteString(":\n\t\treturn true\n\t}\n\treturn false\n}\n\n")
	}
	return b.String()
}

func writeField(b *strings.Builder, m *model, n nodeDef, f fieldDef) {
	meth := camel(f.Name)
	typ := m.fieldType(f)
	what := f.Node
	if what == "" {
		what = f.Token
		if len(f.Tokens) > 0 {
			what = strings.Join(f.Tokens, "|")
		}
	}
	if f.List {
		fmt.Fprintf(b, "// %s returns every %s child (field %s).\n",
			meth, what, f.Name)
		fmt.Fprintf(b, "func (n %s) %s() iter.Seq[%s] {\n", n.Name, meth,
			typ)
		switch {
		case f.Node != "":
			fmt.Fprintf(b, "\treturn n.Node.Children[%s]()\n", typ)
		case f.Token != "":
			fmt.Fprintf(b, "\treturn n.Node.TokensIn(token.NewSet("+
				"token.%s))\n", f.Token)
		default:
			fmt.Fprintf(b, "\treturn n.Node.TokensIn(set%s%s)\n", n.Name,
				meth)
		}
		b.WriteString("}\n\n")
		return
	}
	nth := ""
	switch {
	case f.Last:
		what = "last " + what
	case f.Nth > 0:
		nth = fmt.Sprintf(" number %d (from 0)", f.Nth)
	}
	if f.With != "" {
		nth += " when a " + f.With + " child is present"
	}
	fmt.Fprintf(b, "// %s returns the %s child%s (field %s), or the zero "+
		"value.\n", meth, what, nth, f.Name)
	fmt.Fprintf(b, "func (n %s) %s() %s {\n", n.Name, meth, typ)
	if f.With != "" {
		fmt.Fprintf(b, "\tif n.Node.Token(token.%s, 0).IsZero() {\n"+
			"\t\tvar z %s\n\t\treturn z\n\t}\n", f.With, typ)
	}
	switch {
	case f.Last:
		fmt.Fprintf(b, "\treturn n.Node.LastChild[%s]()\n", typ)
	case f.Node != "":
		fmt.Fprintf(b, "\treturn n.Node.Child[%s](%d)\n", typ, f.Nth)
	case f.Token != "":
		fmt.Fprintf(b, "\treturn n.Node.Token(token.%s, %d)\n", f.Token,
			f.Nth)
	default:
		fmt.Fprintf(b, "\treturn n.Node.TokenIn(set%s%s, %d)\n", n.Name,
			meth, f.Nth)
	}
	b.WriteString("}\n\n")
}

func genEdits(m *model) string {
	var b strings.Builder
	b.WriteString(header + "package parser\n\n")
	b.WriteString("// editTexts are the insertions of the random " +
		"edit-replay tests: every\n// fixed token spelling and every " +
		"keyword.\nvar editTexts = []string{\n")
	for _, t := range m.kinds.Tokens {
		if t.Text != "" {
			fmt.Fprintf(&b, "\t%s,\n", q(t.Text))
		}
	}
	for _, w := range m.words {
		fmt.Fprintf(&b, "\t%s,\n", q(w.Word))
	}
	b.WriteString("}\n")
	return b.String()
}

// genericMethods are the shared accessors of every typed wrapper, Go
// 1.27 generic methods on cst.Node. They live in the generated file
// (golines cannot format generic methods yet).
const genericMethods = `// Child returns the nth (from 0) child node that T accepts, as a T, or
// the zero T.
func (n Node) Child[T View](nth int) T {
	var z T
	for c := range n.Nodes() {
		if z.Accepts(c.Kind()) {
			if nth == 0 {
				return T{c}
			}
			nth--
		}
	}
	return z
}

// LastChild returns the last child node that T accepts, as a T, or the
// zero T.
func (n Node) LastChild[T View]() T {
	var z, last T
	for c := range n.Nodes() {
		if z.Accepts(c.Kind()) {
			last = T{c}
		}
	}
	return last
}

// Children yields every child node that T accepts, as a T.
func (n Node) Children[T View]() iter.Seq[T] {
	return func(yield func(T) bool) {
		var z T
		for c := range n.Nodes() {
			if z.Accepts(c.Kind()) && !yield(T{c}) {
				return
			}
		}
	}
}

`
