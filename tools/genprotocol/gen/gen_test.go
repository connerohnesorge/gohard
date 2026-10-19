package gen

import (
	"bytes"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

func loadModel(t *testing.T) []byte {
	t.Helper()
	b, err := os.ReadFile("../metaModel.json")
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// TestDeterministic: the same model gives the same bytes, and the
// output is gofmt-clean with the header lib.sh recognizes.
func TestDeterministic(t *testing.T) {
	model := loadModel(t)
	a, err := Generate(model)
	if err != nil {
		t.Fatal(err)
	}
	for range 3 {
		b, err := Generate(model)
		if err != nil || !bytes.Equal(a, b) {
			t.Fatal("Generate is not deterministic")
		}
	}
	f, err := format.Source(a)
	if err != nil || !bytes.Equal(f, a) {
		t.Fatal("output is not gofmt-clean")
	}
	header := regexp.MustCompile(`^// Code generated .* DO NOT EDIT\.$`)
	if first, _, _ := strings.Cut(string(a), "\n"); !header.MatchString(
		first) {
		t.Fatalf("header %q does not mark generated code", first)
	}
}

// TestLineLengths: every line the generator controls fits 80 columns
// (tabs as 4, golines' default). Struct fields with tags and comment
// lines holding one long word (a URL) cannot be broken and are exempt,
// as generated files are in scripts/ci/format.sh.
func TestLineLengths(t *testing.T) {
	out, err := Generate(loadModel(t))
	if err != nil {
		t.Fatal(err)
	}
	long := 0
	for i, line := range strings.Split(string(out), "\n") {
		if fits(line) || strings.Contains(line, "`json:\"") {
			continue
		}
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "//") &&
			!strings.Contains(strings.TrimPrefix(trimmed, "// "), " ") {
			continue
		}
		long++
		if long <= 3 {
			t.Logf("line %d: %s", i+1, line)
		}
	}
	// Known exceptions: an untyped-length enum member, two receiver
	// lines of the longest or type, and doc lines ending in a URL.
	if long > 8 {
		t.Errorf("%d lines over 80 columns", long)
	}
}

// constants returns the string constants of a generated file,
// evaluating "a" + "b" concatenations.
func constants(t *testing.T, src []byte) map[string]string {
	t.Helper()
	f, err := parser.ParseFile(token.NewFileSet(), "x.go", src, 0)
	if err != nil {
		t.Fatal(err)
	}
	var eval func(ast.Expr) (string, bool)
	eval = func(e ast.Expr) (string, bool) {
		switch e := e.(type) {
		case *ast.BasicLit:
			s, err := strconv.Unquote(e.Value)
			return s, err == nil
		case *ast.BinaryExpr:
			a, ok1 := eval(e.X)
			b, ok2 := eval(e.Y)
			return a + b, ok1 && ok2
		}
		return "", false
	}
	out := map[string]string{}
	for _, d := range f.Decls {
		g, ok := d.(*ast.GenDecl)
		if !ok || g.Tok != token.CONST {
			continue
		}
		for _, spec := range g.Specs {
			vs := spec.(*ast.ValueSpec)
			for i, n := range vs.Names {
				if i < len(vs.Values) {
					if s, ok := eval(vs.Values[i]); ok {
						out[n.Name] = s
					}
				}
			}
		}
	}
	return out
}

// TestMethodConstants: each method in Methods has its constant.
func TestMethodConstants(t *testing.T) {
	out, err := Generate(loadModel(t))
	if err != nil {
		t.Fatal(err)
	}
	consts := constants(t, out)
	for _, m := range Methods {
		name := "Method" + methodName(m)
		if consts[name] != m {
			t.Errorf("%s = %q, want %q", name, consts[name], m)
		}
	}
}

// mini builds a small model around one structure.
func mini(props string, extra ...string) []byte {
	return []byte(`{"metaData": {"version": "test"},
	"requests": [], "notifications": [{"method": "initialized",
		"messageDirection": "clientToServer",
		"params": {"kind": "reference", "name": "T"}}],
	"structures": [{"name": "T", "properties": [` + props + `]},
		{"name": "S", "properties": [{"name": "a",
			"type": {"kind": "base", "name": "string"}}]}` +
		strings.Join(extra, "") + `],
	"enumerations": [{"name": "Z", "type": {"kind": "base",
		"name": "uinteger"}, "values": [{"name": "none", "value": 0}]},
		{"name": "N", "type": {"kind": "base", "name": "uinteger"},
		"values": [{"name": "one", "value": 1}]}],
	"typeAliases": [{"name": "LSPAny", "type": {"kind": "base",
		"name": "string"}}]}`)
}

func generateMini(t *testing.T, props string) string {
	t.Helper()
	saved := Methods
	Methods = []string{"initialized"}
	savedExtra := Extra
	Extra = nil
	defer func() { Methods, Extra = saved, savedExtra }()
	out, err := Generate(mini(props))
	if err != nil {
		t.Fatal(err)
	}
	return string(out)
}

func TestMappingRules(t *testing.T) {
	base := func(n string) string {
		return `{"kind": "base", "name": "` + n + `"}`
	}
	ref := func(n string) string {
		return `{"kind": "reference", "name": "` + n + `"}`
	}
	prop := func(name, typ string, optional bool) string {
		o := ""
		if optional {
			o = `, "optional": true`
		}
		return `{"name": "` + name + `", "type": ` + typ + o + `}`
	}
	nullOr := func(typ string) string {
		return `{"kind": "or", "items": [` + typ + `, ` + base("null") +
			`]}`
	}
	tests := []struct {
		prop, want string
	}{
		{prop("b", base("boolean"), true), "B *bool `json:\"b,omitempty\"`"},
		{prop("n", base("integer"), false), "N int32 `json:\"n\"`"},
		{prop("u", base("uinteger"), true),
			"U *uint32 `json:\"u,omitempty\"`"},
		{prop("s", base("string"), true), "S string `json:\"s,omitempty\"`"},
		{prop("rootUri", nullOr(base("DocumentUri")), false),
			"RootURI *DocumentURI `json:\"rootUri\"`"},
		{prop("processId", nullOr(base("integer")), false),
			"ProcessID *int32 `json:\"processId\"`"},
		{prop("z", ref("Z"), true), "Z *Z `json:\"z,omitempty\"`"},
		{prop("e", ref("N"), true), "E N `json:\"e,omitempty\"`"},
		{prop("o", ref("S"), true), "O *S `json:\"o,omitempty\"`"},
		{prop("r", ref("S"), false), "R S `json:\"r\"`"},
		{prop("l", `{"kind": "array", "element": `+ref("S")+`}`, false),
			"L []S `json:\"l\"`"},
		{prop("x", `{"kind": "or", "items": [`+base("boolean")+`, `+
			ref("S")+`]}`, true), "X BoolOrS `json:\"x,omitzero\"`"},
		{prop("lit", `{"kind": "literal", "value": {"properties": [`+
			prop("q", base("decimal"), false)+`]}}`, true),
			"Lit *TLit `json:\"lit,omitempty\"`"},
		{prop("t", `{"kind": "tuple", "items": [`+base("uinteger")+`, `+
			base("uinteger")+`]}`, false), "T [2]uint32 `json:\"t\"`"},
		{prop("k", `{"kind": "stringLiteral", "value": "create"}`, false),
			"K string `json:\"k\"`"},
		{prop("m", `{"kind": "map", "key": `+base("DocumentUri")+
			`, "value": `+ref("S")+`}`, true),
			"M map[DocumentURI]S `json:\"m,omitempty\"`"},
	}
	for _, tt := range tests {
		out := generateMini(t, tt.prop)
		norm := regexp.MustCompile(`[ \t]+`).ReplaceAllString(out, " ")
		if !strings.Contains(norm, tt.want) {
			t.Errorf("%s: no line %q in\n%s", tt.prop, tt.want, out)
		}
	}
	// A required list gets a MarshalJSON that writes [] for nil.
	out := generateMini(t, prop("l", `{"kind": "array", "element": `+
		ref("S")+`}`, false))
	if !strings.Contains(out, "p.L = []S{}") {
		t.Errorf("no empty-list MarshalJSON:\n%s", out)
	}
}

func TestErrors(t *testing.T) {
	saved, savedExtra := Methods, Extra
	defer func() { Methods, Extra = saved, savedExtra }()
	Methods = []string{"no/such/method"}
	if _, err := Generate(loadModel(t)); err == nil ||
		!strings.Contains(err.Error(), "no/such/method") {
		t.Errorf("unknown method: %v", err)
	}
	Methods, Extra = []string{"initialized"}, nil
	and := `{"name": "a", "type": {"kind": "and", "items": []}}`
	if _, err := Generate(mini(and)); err == nil ||
		!strings.Contains(err.Error(), `"and"`) {
		t.Errorf("unsupported kind: %v", err)
	}
	if _, err := Generate([]byte(`{`)); err == nil {
		t.Error("invalid JSON accepted")
	}
}

func TestNames(t *testing.T) {
	for in, want := range map[string]string{
		"uri": "URI", "id": "ID", "rootUri": "RootURI",
		"processId": "ProcessID", "valid": "Valid", "Id": "ID",
		"textDocument": "TextDocument",
	} {
		if got := fieldName(in); got != want {
			t.Errorf("fieldName(%q) = %q, want %q", in, got, want)
		}
	}
	for in, want := range map[string]string{
		"$/cancelRequest":                "CancelRequest",
		"textDocument/didOpen":           "TextDocumentDidOpen",
		"window/workDoneProgress/create": "WindowWorkDoneProgressCreate",
	} {
		if got := methodName(in); got != want {
			t.Errorf("methodName(%q) = %q, want %q", in, got, want)
		}
	}
}
