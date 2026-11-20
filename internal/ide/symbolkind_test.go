package ide

import (
	"fmt"
	"maps"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/connerohnesorge/gohard/v2/internal/cst"
	"github.com/connerohnesorge/gohard/v2/internal/protocol"
	"github.com/connerohnesorge/gohard/v2/internal/sema"
	"github.com/connerohnesorge/gohard/v2/internal/server"
)

// TestSymbolKindTable locks the outline table: every declaration kind
// has one row, statement labels are the only rows with a statement,
// and a label the table does not name is a namespace.
func TestSymbolKindTable(t *testing.T) {
	seen := map[sema.DeclKind]bool{}
	keys := map[[2]uint32]bool{}
	for _, row := range symbolKinds {
		key := [2]uint32{uint32(row.decl), uint32(row.stmt)}
		if keys[key] {
			t.Errorf("duplicate row %s stmt %d", row.decl, row.stmt)
		}
		keys[key] = true
		if row.kind < protocol.SymbolKindFile ||
			row.kind > protocol.SymbolKindTypeParameter {
			t.Errorf("%s has kind %d", row.decl, row.kind)
		}
		if row.stmt == 0 {
			seen[row.decl] = true
		}
		if row.decl != sema.DeclLabel && row.stmt != 0 {
			t.Errorf("%s carries a statement kind", row.decl)
		}
	}
	n := 0
	for k := sema.DeclKind(1); ; k++ {
		name := k.String()
		if strings.HasPrefix(name, "DeclKind(") {
			break
		}
		n++
		if !seen[k] {
			t.Errorf("no symbol kind for %s", name)
		}
	}
	if n == 0 {
		t.Fatal("no declaration kinds")
	}
	labels := []struct {
		stmt cst.Kind
		want protocol.SymbolKind
	}{
		{cst.KindProcessStmt, protocol.SymbolKindMethod},
		{cst.KindComponentInstantiationStmt, protocol.SymbolKindObject},
		{cst.KindForGenerateStmt, protocol.SymbolKindModule},
		{cst.KindIfGenerateStmt, protocol.SymbolKindModule},
		{cst.KindCaseGenerateStmt, protocol.SymbolKindModule},
		{cst.KindGenerateBranch, protocol.SymbolKindModule},
		{cst.KindCaseGenerateAlternative, protocol.SymbolKindModule},
		{cst.KindBlockStmt, protocol.SymbolKindModule},
		{cst.KindSequentialBlockStmt, protocol.SymbolKindModule},
		{cst.KindConcurrentProcedureCallStmt,
			protocol.SymbolKindFunction},
		{cst.KindProcedureCallStmt, protocol.SymbolKindFunction},
		{cst.KindLoopStmt, protocol.SymbolKindNamespace},
		{0, protocol.SymbolKindNamespace},
	}
	for _, tc := range labels {
		if got := SymbolKind(sema.DeclLabel, tc.stmt); got != int(tc.want) {
			t.Errorf("label %d: %s, want %s", tc.stmt,
				kindName(got), kindName(int(tc.want)))
		}
	}
	// A statement kind does not change any other declaration.
	if got := SymbolKind(sema.DeclSignal, cst.KindProcessStmt); got !=
		int(protocol.SymbolKindVariable) {
		t.Fatalf("signal kind %s", kindName(got))
	}
	if got := SymbolKind(sema.DeclEntity, 0); got !=
		int(protocol.SymbolKindInterface) {
		t.Fatalf("entity kind %s", kindName(got))
	}
	if got := SymbolKind(sema.DeclPort, 0); got !=
		int(protocol.SymbolKindVariable) {
		t.Fatalf("port kind %s", kindName(got))
	}
}

// TestDocumentSymbolKinds is the SymbolKind a client sees for
// testdata/lsp/symbols/kinds: document symbols, and the same kind on
// every workspace symbol taken from that file.
func TestDocumentSymbolKinds(t *testing.T) {
	const path = "../../testdata/lsp/symbols/kinds/in/kinds.vhd"
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	an := analyze(t, snapshotOf(t, project{"kinds.vhd": b}, nil))
	for _, fd := range an.Diagnostics {
		for _, d := range fd.Diagnostics {
			t.Errorf("%s:%d:%d: %s: %s", d.Path, d.Line, d.Col,
				d.Severity, d.Message)
		}
	}
	ds := an.DocumentSymbols(uri("kinds.vhd"))
	got := flattenSymbols(ds)
	if diff := diffKinds(got, wantSymbolKinds); diff != "" {
		t.Errorf("document symbol kinds:\n%s", diff)
	}
	bySpan := map[spanKey]server.Symbol{}
	var walk func([]server.Symbol)
	walk = func(ss []server.Symbol) {
		for _, s := range ss {
			bySpan[spanKey{s.URI, s.Span.Start, s.Span.End}] = s
			walk(s.Children)
		}
	}
	walk(ds)
	n := 0
	for _, w := range an.WorkspaceSymbols("") {
		if w.URI != uri("kinds.vhd") {
			continue
		}
		n++
		d, ok := bySpan[spanKey{w.URI, w.Span.Start, w.Span.End}]
		if !ok || d.Name != w.Name || d.Kind != w.Kind {
			t.Errorf("workspace %s kind %s span %d:%d, document %+v",
				w.Name, kindName(w.Kind), w.Span.Start, w.Span.End, d)
		}
	}
	if n == 0 {
		t.Fatal("no workspace symbols")
	}
}

type spanKey struct {
	uri        string
	start, end int
}

func flattenSymbols(ss []server.Symbol) map[string]int {
	out := map[string]int{}
	var walk func([]server.Symbol, string)
	walk = func(ss []server.Symbol, prefix string) {
		count := map[string]int{}
		for _, s := range ss {
			count[s.Name]++
		}
		for _, s := range ss {
			name := s.Name
			if count[s.Name] > 1 {
				name = s.Detail
			}
			if prefix != "" {
				name = prefix + "/" + name
			}
			if _, ok := out[name]; ok {
				name = name + " @" + s.Detail
			}
			out[name] = s.Kind
			walk(s.Children, name)
		}
	}
	walk(ss, "")
	return out
}

func diffKinds(got, want map[string]int) string {
	keys := map[string]struct{}{}
	for k := range got {
		keys[k] = struct{}{}
	}
	for k := range want {
		keys[k] = struct{}{}
	}
	var b strings.Builder
	for _, k := range slices.Sorted(maps.Keys(keys)) {
		g, gok := got[k]
		w, wok := want[k]
		switch {
		case !gok:
			fmt.Fprintf(&b, "missing %s (want %s)\n", k, kindName(w))
		case !wok:
			fmt.Fprintf(&b, "unexpected %s = %s\n", k, kindName(g))
		case g != w:
			fmt.Fprintf(&b, "%s = %s, want %s\n", k, kindName(g),
				kindName(w))
		}
	}
	return b.String()
}

func kindName(k int) string {
	switch protocol.SymbolKind(k) {
	case protocol.SymbolKindFile:
		return "File"
	case protocol.SymbolKindModule:
		return "Module"
	case protocol.SymbolKindNamespace:
		return "Namespace"
	case protocol.SymbolKindPackage:
		return "Package"
	case protocol.SymbolKindClass:
		return "Class"
	case protocol.SymbolKindMethod:
		return "Method"
	case protocol.SymbolKindProperty:
		return "Property"
	case protocol.SymbolKindField:
		return "Field"
	case protocol.SymbolKindConstructor:
		return "Constructor"
	case protocol.SymbolKindEnum:
		return "Enum"
	case protocol.SymbolKindInterface:
		return "Interface"
	case protocol.SymbolKindFunction:
		return "Function"
	case protocol.SymbolKindVariable:
		return "Variable"
	case protocol.SymbolKindConstant:
		return "Constant"
	case protocol.SymbolKindObject:
		return "Object"
	case protocol.SymbolKindKey:
		return "Key"
	case protocol.SymbolKindEnumMember:
		return "EnumMember"
	case protocol.SymbolKindStruct:
		return "Struct"
	case protocol.SymbolKindEvent:
		return "Event"
	case protocol.SymbolKindTypeParameter:
		return "TypeParameter"
	default:
		return fmt.Sprintf("kind(%d)", k)
	}
}

// wantSymbolKinds is the SymbolKind a client sees for kinds.vhd, keyed
// by the symbol's path. A package and its body share a name, so those
// two use the declaration's detail ("package ops", "package body ops").
// The context's own declaration is also a child of its region, which
// is why "demo" appears twice.
var wantSymbolKinds = map[string]int{
	"boxed":                    int(protocol.SymbolKindInterface),
	"boxed/data_t":             int(protocol.SymbolKindTypeParameter),
	"boxed/same":               int(protocol.SymbolKindFunction),
	"demo":                     int(protocol.SymbolKindNamespace),
	"demo/demo":                int(protocol.SymbolKindNamespace),
	"gp3":                      int(protocol.SymbolKindPackage),
	"gp3/bias":                 int(protocol.SymbolKindConstant),
	"gp3/n":                    int(protocol.SymbolKindConstant),
	"gp3/show":                 int(protocol.SymbolKindFunction),
	"package body gp":          int(protocol.SymbolKindPackage),
	"package body gp/show":     int(protocol.SymbolKindFunction),
	"package body ops":         int(protocol.SymbolKindPackage),
	"package body ops/inc":     int(protocol.SymbolKindFunction),
	"package body ops/step":    int(protocol.SymbolKindFunction),
	"package gp":               int(protocol.SymbolKindPackage),
	"package gp/bias":          int(protocol.SymbolKindConstant),
	"package gp/n":             int(protocol.SymbolKindConstant),
	"package gp/show":          int(protocol.SymbolKindFunction),
	"package ops":              int(protocol.SymbolKindPackage),
	"package ops/NIL":          int(protocol.SymbolKindProperty),
	"package ops/ZERO":         int(protocol.SymbolKindConstant),
	"package ops/busy":         int(protocol.SymbolKindEnumMember),
	"package ops/byte_t":       int(protocol.SymbolKindStruct),
	"package ops/idle":         int(protocol.SymbolKindEnumMember),
	"package ops/inc":          int(protocol.SymbolKindFunction),
	"package ops/keep":         int(protocol.SymbolKindProperty),
	"package ops/leaf":         int(protocol.SymbolKindInterface),
	"package ops/leaf/WIDTH":   int(protocol.SymbolKindConstant),
	"package ops/leaf/clk":     int(protocol.SymbolKindVariable),
	"package ops/pair_t":       int(protocol.SymbolKindStruct),
	"package ops/pair_t/first": int(protocol.SymbolKindField),
	"package ops/pair_t/second": int(
		protocol.SymbolKindField),
	"package ops/span_t":  int(protocol.SymbolKindStruct),
	"package ops/state_t": int(protocol.SymbolKindStruct),
	"package ops/step":    int(protocol.SymbolKindFunction),
	"package ops/tick":    int(protocol.SymbolKindEnumMember),
	"package ops/tock":    int(protocol.SymbolKindEnumMember),
	"rtl":                 int(protocol.SymbolKindModule),
	"rtl/LIMIT":           int(protocol.SymbolKindConstant),
	"rtl/blk":             int(protocol.SymbolKindModule),
	"rtl/blk/K":           int(protocol.SymbolKindConstant),
	"rtl/call_step":       int(protocol.SymbolKindFunction),
	"rtl/g":               int(protocol.SymbolKindModule),
	"rtl/g/i":             int(protocol.SymbolKindConstant),
	"rtl/hi":              int(protocol.SymbolKindModule),
	"rtl/lo":              int(protocol.SymbolKindModule),
	"rtl/logf":            int(protocol.SymbolKindFile),
	"rtl/n":               int(protocol.SymbolKindVariable),
	"rtl/p":               int(protocol.SymbolKindMethod),
	"rtl/p/lp":            int(protocol.SymbolKindNamespace),
	"rtl/p/lp/i":          int(protocol.SymbolKindConstant),
	"rtl/p/v":             int(protocol.SymbolKindVariable),
	"rtl/u0":              int(protocol.SymbolKindObject),
	"rtl/wide":            int(protocol.SymbolKindModule),
	"top":                 int(protocol.SymbolKindInterface),
	"top/WIDTH":           int(protocol.SymbolKindConstant),
	"top/clk":             int(protocol.SymbolKindVariable),
	"top/q":               int(protocol.SymbolKindVariable),
	"top_cfg":             int(protocol.SymbolKindModule),
}
