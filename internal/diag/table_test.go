package diag

import (
	"go/ast"
	"go/parser"
	"go/token"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
)

var (
	codeRE  = regexp.MustCompile(`^[a-z0-9]+/[a-z0-9]+(-[a-z0-9]+)*$`)
	paramRE = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)
)

func TestTableWellFormed(t *testing.T) {
	seen := map[Code]bool{}
	for i, info := range table {
		c := info.Code
		if !codeRE.MatchString(string(c)) {
			t.Errorf("%q: malformed code", c)
		}
		if seen[c] {
			t.Errorf("%q: duplicate code", c)
		}
		seen[c] = true
		if i > 0 && table[i-1].Code >= c {
			t.Errorf("%q: table not sorted (after %q)",
				c, table[i-1].Code)
		}
		if info.Severity < Error || info.Severity > Hint {
			t.Errorf("%q: bad severity %d", c, info.Severity)
		}
		switch info.Class {
		case Tool:
			if info.Severity != Error {
				t.Errorf("%q: tool class must be an error", c)
			}
		case Unchecked:
			if info.Severity != Warning {
				t.Errorf("%q: unchecked class must be a warning", c)
			}
		case Design:
		default:
			t.Errorf("%q: bad class %d", c, info.Class)
		}
		if info.Owner <= 0 || info.Summary == "" || info.Message == "" {
			t.Errorf("%q: missing owner, summary or message", c)
		}
		checkTemplate(t, c, info.Message)
		for _, v := range info.Variants {
			checkTemplate(t, c, v.Message)
			if !slices.Equal(params(v.Message), info.Params()) {
				t.Errorf("%q variant %q: params %v differ from %v",
					c, v.Name, params(v.Message), info.Params())
			}
		}
	}
}

func checkTemplate(t *testing.T, c Code, msg string) {
	t.Helper()
	depth := 0
	for _, r := range msg {
		switch r {
		case '{':
			depth++
		case '}':
			depth--
		}
		if depth < 0 || depth > 1 {
			t.Errorf("%q: unbalanced braces in %q", c, msg)
			return
		}
	}
	if depth != 0 {
		t.Errorf("%q: unbalanced braces in %q", c, msg)
	}
	for _, p := range params(msg) {
		if !paramRE.MatchString(p) {
			t.Errorf("%q: bad parameter name %q", c, p)
		}
	}
	if msg[len(msg)-1] == '.' || msg[len(msg)-1] == '\n' {
		t.Errorf("%q: message must not end in '.' or newline", c)
	}
}

// TestConstantsMatchTable parses table.go and checks that every Code
// constant has a row and every row has a constant.
func TestConstantsMatchTable(t *testing.T) {
	f, err := parser.ParseFile(token.NewFileSet(), "table.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	var consts []Code
	for _, decl := range f.Decls {
		gd, ok := decl.(*ast.GenDecl)
		if !ok || gd.Tok != token.CONST {
			continue
		}
		for _, spec := range gd.Specs {
			vs := spec.(*ast.ValueSpec)
			id, ok := vs.Type.(*ast.Ident)
			if !ok || id.Name != "Code" {
				continue
			}
			lit := vs.Values[0].(*ast.BasicLit)
			s, _ := strconv.Unquote(lit.Value)
			consts = append(consts, Code(s))
		}
	}
	slices.Sort(consts)
	var rows []Code
	for _, info := range table {
		rows = append(rows, info.Code)
	}
	if !slices.Equal(consts, rows) {
		t.Fatalf("constants %v\n!= table %v", consts, rows)
	}
}

// TestRequiredCodes pins the codes the issues name explicitly.
func TestRequiredCodes(t *testing.T) {
	want := map[Code]struct {
		sev   Severity
		class Class
	}{
		"syntax/standard":            {Error, Design},
		"syntax/undefined-condition": {Error, Design},
		"unsupported/construct":      {Error, Tool},
		"workspace/file-too-large":   {Warning, Design},
		"sim/budget-exceeded":        {Error, Design},
		"config/redundant-default":   {Warning, Design},
		"config/builtin-define":      {Error, Tool},
		"cli/usage":                  {Error, Tool},
		"sema/eval-limit":            {Warning, Design},
		"sema/unchecked-unit":        {Warning, Unchecked},
		"syntax/utf8-comment":        {Hint, Design},
		"sim/race":                   {Warning, Design},
		"sim/assert-note":            {Hint, Design},
		"repro/source-changed":       {Error, Tool},
		"stdlib/integrity":           {Error, Tool},
		"internal/crash":             {Error, Tool},
		"library/folder-missing":     {Error, Tool},
		"workspace/symlink-loop":     {Error, Tool},
		"cli/no-files":               {Error, Tool},
		"cli/missing-path":           {Error, Tool},
		"format/would-change":        {Error, Design},
	}
	for c, w := range want {
		info, ok := c.Info()
		if !ok {
			t.Errorf("%q missing from the table", c)
			continue
		}
		if info.Severity != w.sev || info.Class != w.class {
			t.Errorf("%q: %v/%v, want %v/%v", c, info.Severity,
				info.Class, w.sev, w.class)
		}
	}
}

func TestUnsupportedMessages(t *testing.T) {
	d := NewVariant(UnsupportedConstruct, PleaseReport, "group template")
	if d.Message != "group template is not supported yet, please report" {
		t.Errorf("report message = %q", d.Message)
	}
	if New(UnsupportedConstruct, "x").Message !=
		NewVariant(UnsupportedConstruct, PleaseReport, "x").Message {
		t.Error("default message must be the please-report variant")
	}
	// The v1.0 variant naming the v1.1 milestone is retired with the
	// v1.1 release: no variant names a milestone.
	info, _ := UnsupportedConstruct.Info()
	for _, v := range info.Variants {
		if strings.Contains(v.Message, "v1.") {
			t.Errorf("variant %s: %q", v.Name, v.Message)
		}
	}
}

func TestFormat(t *testing.T) {
	info, _ := ConfigUnquotedBool.Info()
	if got := info.Params(); !slices.Equal(got,
		[]string{"key", "value"}) {
		t.Fatalf("Params = %v", got)
	}
	got := info.Format("defines.FOO", "yes")
	want := `defines.FOO: yes reads as a YAML 1.1 boolean; quote it: "yes"`
	if got != want {
		t.Errorf("Format = %q, want %q", got, want)
	}
	// Arguments are never re-expanded and missing ones stay visible.
	if got := info.Format("{value}"); got != `{value}: {value} reads `+
		`as a YAML 1.1 boolean; quote it: "{value}"` {
		t.Errorf("Format with brace argument = %q", got)
	}
	if d := New("no/such-code"); d.Code != InternalUnknownCode ||
		d.Message != "internal error: unknown diagnostic code "+
			"no/such-code" {
		t.Errorf("unknown code gave %+v", d)
	}
}

func TestEveryTemplateFills(t *testing.T) {
	for _, info := range Table() {
		args := make([]string, len(info.Params()))
		for i := range args {
			args[i] = "ARG" + strconv.Itoa(i)
		}
		msg := info.Format(args...)
		if regexp.MustCompile(`\{[a-z_0-9]+\}`).MatchString(msg) {
			t.Errorf("%q: unfilled parameter in %q", info.Code, msg)
		}
		d := New(info.Code, args...)
		if d.Severity != info.Severity || d.Message != msg {
			t.Errorf("%q: New = %+v", info.Code, d)
		}
	}
}
