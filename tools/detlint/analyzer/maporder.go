package analyzer

import (
	"go/ast"
	"go/token"
	"go/types"

	"golang.org/x/tools/go/ast/inspector"
	"golang.org/x/tools/go/types/typeutil"
)

// funcName returns "pkgpath.Name" when call calls a package-level
// function, and "" otherwise.
func (c *checker) funcName(call *ast.CallExpr) string {
	fn, ok := typeutil.Callee(c.pass.TypesInfo, call).(*types.Func)
	if !ok || fn.Pkg() == nil {
		return ""
	}
	if sig, ok := fn.Type().(*types.Signature); !ok || sig.Recv() != nil {
		return ""
	}
	return fn.Pkg().Path() + "." + fn.Name()
}

var mapIters = map[string]bool{
	"maps.Keys": true, "maps.Values": true, "maps.All": true,
}

// isMapIter reports whether e is a call to maps.Keys, Values or All.
func (c *checker) isMapIter(e ast.Expr) bool {
	call, ok := ast.Unparen(e).(*ast.CallExpr)
	return ok && mapIters[c.funcName(call)]
}

// isMap reports whether ranging over a value of type t visits a map:
// t is a map, or a type parameter whose type set holds only maps.
func isMap(t types.Type) bool {
	if t == nil {
		return false
	}
	if tp, ok := types.Unalias(t).(*types.TypeParam); ok {
		return onlyMaps(tp.Constraint())
	}
	_, ok := t.Underlying().(*types.Map)
	return ok
}

func onlyMaps(t types.Type) bool {
	iface, ok := t.Underlying().(*types.Interface)
	if !ok {
		_, ok := t.Underlying().(*types.Map)
		return ok
	}
	for i := range iface.NumEmbeddeds() {
		switch e := iface.EmbeddedType(i).(type) {
		case *types.Union:
			all := e.Len() > 0
			for j := range e.Len() {
				_, m := e.Term(j).Type().Underlying().(*types.Map)
				all = all && m
			}
			if all {
				return true
			}
		default:
			if onlyMaps(e) {
				return true
			}
		}
	}
	return false
}

func blank(e ast.Expr) bool {
	id, ok := e.(*ast.Ident)
	return e == nil || ok && id.Name == "_"
}

func (c *checker) rangeStmt(cur inspector.Cursor, n *ast.RangeStmt) {
	if !isMap(c.pass.TypesInfo.TypeOf(n.X)) && !c.isMapIter(n.X) {
		return
	}
	if n.Tok == token.ASSIGN && (!blank(n.Key) || !blank(n.Value)) {
		c.report(n, MapOrder,
			"range over a map assigns to variables outside the loop")
		return
	}
	b := &body{c: c, loop: n}
	if !b.stmts(n.Body.List, false) {
		c.report(n, MapOrder,
			"range over a map whose body depends on iteration order")
		return
	}
	for i, v := range b.appends {
		if !c.sortedNext(cur, v) {
			c.report(b.sites[i], MapOrder, "append to "+v.Name()+
				" in map order without a later sort")
		}
	}
}

var sortedConsumers = map[string]bool{
	"slices.Sorted": true, "slices.SortedFunc": true,
	"slices.SortedStableFunc": true, "maps.Collect": true,
	"maps.Insert": true,
}

// mapIterValue reports maps.Keys, Values or All used other than as the
// function of a call, such as k := maps.Keys[M]: calls through such a
// value are not seen by mapsIterCall, so the value itself is banned.
func (c *checker) mapIterValue(cur inspector.Cursor, id *ast.Ident) {
	fn, ok := c.pass.TypesInfo.Uses[id].(*types.Func)
	if !ok || fn.Pkg() == nil || !mapIters[fn.Pkg().Path()+"."+fn.Name()] {
		return
	}
	var fun ast.Node = id
	p := cur.Parent()
	if sel, ok := p.Node().(*ast.SelectorExpr); ok && sel.Sel == id {
		fun, p = sel, p.Parent()
	}
	switch ix := p.Node().(type) { // explicit instantiation
	case *ast.IndexExpr:
		if ix.X == fun {
			fun, p = ix, p.Parent()
		}
	case *ast.IndexListExpr:
		if ix.X == fun {
			fun, p = ix, p.Parent()
		}
	}
	for {
		paren, ok := p.Node().(*ast.ParenExpr)
		if !ok {
			break
		}
		fun, p = paren, p.Parent()
	}
	if call, ok := p.Node().(*ast.CallExpr); ok && call.Fun == fun {
		return // judged by mapsIterCall or rangeStmt
	}
	c.report(id, MapOrder, "maps."+fn.Name()+
		" used as a function value, which yields in map order")
}

// mapsIterCall checks a maps.Keys, Values or All call outside a range.
func (c *checker) mapsIterCall(cur inspector.Cursor, call *ast.CallExpr) {
	name := c.funcName(call)
	if !mapIters[name] {
		return
	}
	parent := cur.Parent()
	switch p := parent.Node().(type) {
	case *ast.RangeStmt:
		if ast.Unparen(p.X) == call {
			return // judged by rangeStmt
		}
	case *ast.CallExpr:
		outer := c.funcName(p)
		if sortedConsumers[outer] {
			return
		}
		if outer == "slices.Collect" && c.collectSorted(parent, p) {
			return
		}
	}
	c.report(call, MapOrder, name+" yields in map order")
}

// collectSorted reports whether the slices.Collect call at cur is
// assigned to a single variable that is then sorted (sortedNext).
func (c *checker) collectSorted(cur inspector.Cursor,
	call *ast.CallExpr,
) bool {
	var lhs ast.Expr
	switch p := cur.Parent().Node().(type) {
	case *ast.AssignStmt:
		if len(p.Lhs) == 1 && len(p.Rhs) == 1 && p.Rhs[0] == call {
			lhs = p.Lhs[0]
		}
	case *ast.ValueSpec:
		if len(p.Names) == 1 && len(p.Values) == 1 && p.Values[0] == call {
			lhs = p.Names[0]
		}
	}
	id, ok := lhs.(*ast.Ident)
	if !ok {
		return false
	}
	v := c.pass.TypesInfo.ObjectOf(id)
	return v != nil && c.sortedNext(cur, v)
}

// body judges whether a map range body is order-independent.
type body struct {
	c       *checker
	loop    *ast.RangeStmt
	appends []types.Object // outer slices appended to, first-seen order
	sites   []ast.Node
	// ops maps each outer target (by expression text) to the one kind
	// of order-independent update the body applies to it. Two kinds on
	// one target (x += v; x *= 3, or max then min, or two different
	// constants) do not commute, so the result depends on the order.
	ops map[string]string
}

// op records that the body updates outer target lhs with kind and
// reports whether that is still the only kind applied to it.
func (b *body) op(lhs ast.Expr, kind string) bool {
	if b.ops == nil {
		b.ops = map[string]string{}
	}
	key := types.ExprString(ast.Unparen(lhs))
	if prev, ok := b.ops[key]; ok && prev != kind {
		return false
	}
	b.ops[key] = kind
	return true
}

// opKind groups the update operators that commute with each other.
var opKind = map[token.Token]string{
	token.ADD_ASSIGN: "add", token.SUB_ASSIGN: "add",
	token.INC: "add", token.DEC: "add",
	token.MUL_ASSIGN: "mul",
	token.OR_ASSIGN:  "or",
	token.AND_ASSIGN: "and", token.AND_NOT_ASSIGN: "and",
	token.XOR_ASSIGN: "xor",
}

// isLoopKey reports whether e is the key variable the map range
// declares: keys are distinct, so a write indexed by it hits a
// different slot on every iteration.
func (b *body) isLoopKey(e ast.Expr) bool {
	id, ok := ast.Unparen(e).(*ast.Ident)
	key, kok := b.loop.Key.(*ast.Ident)
	if !ok || !kok || blank(key) || b.loop.Tok != token.DEFINE {
		return false
	}
	info := b.c.pass.TypesInfo
	return info.ObjectOf(id) != nil && info.ObjectOf(id) == info.ObjectOf(key)
}

func (b *body) stmts(list []ast.Stmt, inner bool) bool {
	for _, s := range list {
		if !b.stmt(s, inner) {
			return false
		}
	}
	return true
}

func (b *body) opt(s ast.Stmt, inner bool) bool {
	return s == nil || b.stmt(s, inner)
}

func (b *body) stmt(s ast.Stmt, inner bool) bool {
	switch s := s.(type) {
	case *ast.EmptyStmt:
		return true
	case *ast.BlockStmt:
		return b.stmts(s.List, inner)
	case *ast.AssignStmt:
		return b.assign(s)
	case *ast.IncDecStmt:
		return b.local(s.X) || isNumeric(b.c.pass.TypesInfo.TypeOf(s.X)) &&
			b.pure(s.X) && b.op(s.X, opKind[s.Tok])
	case *ast.ExprStmt:
		call, ok := s.X.(*ast.CallExpr)
		return ok && b.builtin(call) == "delete" && b.pure(call.Args...)
	case *ast.DeclStmt:
		return b.decl(s)
	case *ast.IfStmt:
		return b.opt(s.Init, inner) && b.pure(s.Cond) &&
			b.stmts(s.Body.List, inner) && b.opt(s.Else, inner)
	case *ast.SwitchStmt:
		return b.opt(s.Init, inner) && (s.Tag == nil || b.pure(s.Tag)) &&
			b.clauses(s.Body)
	case *ast.TypeSwitchStmt:
		return b.opt(s.Init, inner) && b.typeSwitchAssign(s.Assign) &&
			b.clauses(s.Body)
	case *ast.ForStmt:
		return b.opt(s.Init, true) && (s.Cond == nil || b.pure(s.Cond)) &&
			b.opt(s.Post, true) && b.stmts(s.Body.List, true)
	case *ast.RangeStmt:
		return b.innerRange(s)
	case *ast.BranchStmt:
		if s.Label != nil {
			return false
		}
		switch s.Tok {
		case token.CONTINUE, token.FALLTHROUGH:
			return true
		case token.BREAK:
			return inner
		}
		return false
	case *ast.ReturnStmt:
		for _, r := range s.Results {
			if !b.constant(r) {
				return false
			}
		}
		return true
	}
	return false
}

func (b *body) innerRange(s *ast.RangeStmt) bool {
	if isMap(b.c.pass.TypesInfo.TypeOf(s.X)) || b.c.isMapIter(s.X) {
		return true // judged on its own by rangeStmt
	}
	if s.Tok == token.ASSIGN && (!b.local(s.Key) && !blank(s.Key) ||
		!b.local(s.Value) && !blank(s.Value)) {
		return false
	}
	return b.pure(s.X) && b.stmts(s.Body.List, true)
}

func (b *body) clauses(list *ast.BlockStmt) bool {
	for _, cl := range list.List {
		cc, ok := cl.(*ast.CaseClause)
		if !ok || !b.pure(cc.List...) || !b.stmts(cc.Body, true) {
			return false
		}
	}
	return true
}

func (b *body) typeSwitchAssign(s ast.Stmt) bool {
	switch s := s.(type) {
	case *ast.ExprStmt:
		return b.pure(s.X)
	case *ast.AssignStmt:
		return len(s.Rhs) == 1 && b.pure(s.Rhs[0])
	}
	return false
}

func (b *body) decl(s *ast.DeclStmt) bool {
	gd, ok := s.Decl.(*ast.GenDecl)
	if !ok {
		return false
	}
	for _, spec := range gd.Specs {
		if vs, ok := spec.(*ast.ValueSpec); ok && !b.pure(vs.Values...) {
			return false
		}
	}
	return true
}

var commutativeOps = map[token.Token]bool{
	token.ADD_ASSIGN: true, token.SUB_ASSIGN: true,
	token.MUL_ASSIGN: true, token.OR_ASSIGN: true,
	token.AND_ASSIGN: true, token.XOR_ASSIGN: true,
	token.AND_NOT_ASSIGN: true,
}

func (b *body) assign(s *ast.AssignStmt) bool {
	switch {
	case s.Tok == token.DEFINE:
		for _, l := range s.Lhs {
			if !blank(l) && !b.local(l) {
				return false // redeclares an outer variable
			}
		}
		return b.pure(s.Rhs...)
	case s.Tok == token.ASSIGN && len(s.Lhs) == len(s.Rhs):
		for i := range s.Lhs {
			if !b.pair(s.Lhs[i], s.Rhs[i]) {
				return false
			}
		}
		return true
	case s.Tok == token.ASSIGN:
		for _, l := range s.Lhs {
			if !blank(l) && !b.local(l) {
				return false
			}
		}
		return b.pure(s.Rhs...)
	case commutativeOps[s.Tok]:
		if b.local(s.Lhs[0]) {
			return b.pure(s.Rhs...)
		}
		t := b.c.pass.TypesInfo.TypeOf(s.Lhs[0])
		return isInteger(t) && b.pure(s.Lhs[0]) && b.pure(s.Rhs...) &&
			b.op(s.Lhs[0], opKind[s.Tok])
	}
	return false
}

func (b *body) pair(lhs, rhs ast.Expr) bool {
	call, isCall := ast.Unparen(rhs).(*ast.CallExpr)
	if isCall && b.builtin(call) == "append" && b.local(lhs) {
		// Growing a slice that lives inside one iteration.
		return sameExpr(call.Args[0], lhs) && b.pure(call.Args[1:]...)
	}
	if blank(lhs) || b.local(lhs) {
		return b.pure(rhs)
	}
	if isCall {
		switch b.builtin(call) {
		case "append":
			return b.appendTo(lhs, call)
		case "min", "max":
			same := false
			for _, a := range call.Args {
				same = same || sameExpr(a, lhs)
			}
			return same && b.pure(lhs) && b.pure(call.Args...) &&
				b.op(lhs, b.builtin(call))
		}
	}
	if ix, ok := ast.Unparen(lhs).(*ast.IndexExpr); ok &&
		b.isLoopKey(ix.Index) {
		return b.pure(lhs, rhs) && b.op(lhs, "key")
	}
	if !b.constant(rhs) || !b.pure(lhs) {
		return false
	}
	return b.op(lhs, "const:"+b.constText(rhs))
}

// constText is the value of a constant expression, for comparing two
// constant stores.
func (b *body) constText(e ast.Expr) string {
	tv := b.c.pass.TypesInfo.Types[e]
	if tv.Value == nil {
		return "nil"
	}
	return tv.Value.ExactString()
}

func (b *body) appendTo(lhs ast.Expr, call *ast.CallExpr) bool {
	id, ok := ast.Unparen(lhs).(*ast.Ident)
	if !ok || len(call.Args) == 0 || !sameExpr(call.Args[0], id) {
		return false
	}
	if !b.pure(call.Args[1:]...) {
		return false
	}
	v := b.c.pass.TypesInfo.ObjectOf(id)
	if _, ok := v.(*types.Var); !ok {
		return false
	}
	for _, seen := range b.appends {
		if seen == v {
			return true
		}
	}
	b.appends = append(b.appends, v)
	b.sites = append(b.sites, call)
	return true
}

func sameExpr(a, b ast.Expr) bool {
	return types.ExprString(ast.Unparen(a)) == types.ExprString(ast.Unparen(b))
}

// local reports whether e is rooted at a variable declared inside the
// loop (including the loop's own key and value) and writing it cannot
// reach memory outside the iteration: no pointer dereference, and no
// field, element or entry reached through a pointer, slice or map,
// which may alias outer state (p := &x; *p = v).
func (b *body) local(e ast.Expr) bool {
	for {
		switch x := e.(type) {
		case *ast.ParenExpr:
			e = x.X
		case *ast.SelectorExpr:
			if b.aliases(x.X) {
				return false
			}
			e = x.X
		case *ast.IndexExpr:
			if !b.pure(x.Index) || b.aliases(x.X) {
				return false
			}
			e = x.X
		case *ast.StarExpr:
			return false
		case *ast.Ident:
			v, ok := b.c.pass.TypesInfo.ObjectOf(x).(*types.Var)
			return ok && v.Pos() >= b.loop.Pos() && v.Pos() < b.loop.End()
		default:
			return false
		}
	}
}

// aliases reports whether writing through e may write shared memory:
// e is a pointer, slice or map.
func (b *body) aliases(e ast.Expr) bool {
	t := b.c.pass.TypesInfo.TypeOf(e)
	if t == nil {
		return true
	}
	switch t.Underlying().(type) {
	case *types.Pointer, *types.Slice, *types.Map:
		return true
	}
	return false
}

func (b *body) constant(e ast.Expr) bool {
	tv, ok := b.c.pass.TypesInfo.Types[e]
	return ok && (tv.Value != nil || tv.IsNil())
}

func (b *body) builtin(call *ast.CallExpr) string {
	fun := ast.Unparen(call.Fun)
	id, ok := fun.(*ast.Ident)
	if !ok {
		return ""
	}
	if bi, ok := b.c.pass.TypesInfo.Uses[id].(*types.Builtin); ok {
		return bi.Name()
	}
	return ""
}

var pureBuiltins = map[string]bool{
	"len": true, "cap": true, "min": true, "max": true, "complex": true,
	"real": true, "imag": true, "make": true, "new": true,
	"Sizeof": true, "Alignof": true, "Offsetof": true,
}

var purePkgs = map[string]bool{
	"strings": true, "strconv": true, "unicode": true,
	"unicode/utf8": true, "unicode/utf16": true, "math": true,
	"math/bits": true, "cmp": true, "path": true, "errors": true,
	"bytes": true,
}

var pureFuncs = map[string]bool{
	"fmt.Sprint": true, "fmt.Sprintf": true, "fmt.Sprintln": true,
	"fmt.Errorf": true,

	"slices.Contains": true, "slices.ContainsFunc": true,
	"slices.Index": true, "slices.IndexFunc": true, "slices.Equal": true,
	"slices.EqualFunc": true, "slices.Compare": true,
	"slices.CompareFunc": true, "slices.Max": true, "slices.MaxFunc": true,
	"slices.Min": true, "slices.MinFunc": true,
	"slices.BinarySearch": true, "slices.BinarySearchFunc": true,
	"slices.Clone": true, "slices.IsSorted": true,
	"slices.IsSortedFunc": true,

	"maps.Clone": true, "maps.Equal": true, "maps.EqualFunc": true,

	"path/filepath.Join": true, "path/filepath.Base": true,
	"path/filepath.Dir": true, "path/filepath.Ext": true,
	"path/filepath.Clean": true, "path/filepath.Rel": true,
	"path/filepath.ToSlash": true, "path/filepath.FromSlash": true,
	"path/filepath.Split": true, "path/filepath.IsAbs": true,
	"path/filepath.Match": true, "path/filepath.IsLocal": true,
	"path/filepath.VolumeName": true, "path/filepath.SplitList": true,
}

// pure reports whether evaluating every expression has no effect that
// could depend on iteration order.
func (b *body) pure(es ...ast.Expr) bool {
	ok := true
	for _, e := range es {
		if e == nil {
			continue
		}
		ast.Inspect(e, func(n ast.Node) bool {
			switch n := n.(type) {
			case *ast.FuncLit:
				ok = false
			case *ast.UnaryExpr:
				ok = ok && n.Op != token.ARROW
			case *ast.CallExpr:
				ok = ok && b.pureCall(n)
			}
			return ok
		})
	}
	return ok
}

func (b *body) pureCall(call *ast.CallExpr) bool {
	info := b.c.pass.TypesInfo
	if tv, ok := info.Types[call.Fun]; ok && tv.IsType() {
		return true // conversion
	}
	switch fn := typeutil.Callee(info, call).(type) {
	case *types.Builtin:
		return pureBuiltins[fn.Name()]
	case *types.Func:
		sig, _ := fn.Type().(*types.Signature)
		if sig != nil && sig.Recv() != nil {
			name := fn.Name()
			return len(call.Args) == 0 && (name == "String" || name == "Error")
		}
		if fn.Pkg() == nil {
			return false
		}
		return purePkgs[fn.Pkg().Path()] ||
			pureFuncs[fn.Pkg().Path()+"."+fn.Name()]
	}
	return false
}

func basicInfo(t types.Type) types.BasicInfo {
	if t == nil {
		return 0
	}
	if bt, ok := t.Underlying().(*types.Basic); ok {
		return bt.Info()
	}
	return 0
}

func isInteger(t types.Type) bool { return basicInfo(t)&types.IsInteger != 0 }

func isNumeric(t types.Type) bool { return basicInfo(t)&types.IsNumeric != 0 }
