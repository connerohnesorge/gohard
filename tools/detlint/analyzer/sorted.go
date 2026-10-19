package analyzer

import (
	"go/ast"
	"go/token"
	"go/types"

	"golang.org/x/tools/go/ast/inspector"
)

// sortedNext reports whether the slice variable v, filled in map order
// by the statement at cur, is put into a total order before anything
// can observe it. That holds when, in the statement list holding cur,
// a later statement is an unconditional sort of the whole of v with a
// total order (see totalSort), and every statement between the two
// neither mentions v nor leaves the list early (return, break,
// continue, goto, labels), so the sort always runs and runs first.
//
// A sort in a branch, after a copy or an output of v, of a subslice
// (v[:1]), or with a comparator that can tie distinct elements, leaves
// map order in place and is not accepted.
func (c *checker) sortedNext(cur inspector.Cursor, v types.Object) bool {
	for {
		list, i, owner := stmtList(cur)
		if list == nil {
			return false
		}
		for _, s := range list[i+1:] {
			if es, ok := s.(*ast.ExprStmt); ok {
				if call, ok := ast.Unparen(es.X).(*ast.CallExpr); ok &&
					c.totalSort(call, v) {
					return true
				}
			}
			if c.mentions(s, v) || leaves(s) {
				return false
			}
		}
		// The list ended unsorted: the sort may follow the statement
		// around it. Leaving a loop body is allowed only when nothing
		// else in the loop sees v, since a later iteration runs the
		// statements before cur again before the sort.
		next, ok := c.climb(owner, list, i, v)
		if !ok {
			return false
		}
		cur = next
	}
}

// climb returns the statement that encloses the statement list owned
// by owner (cur is list[i]), if the end of that list falls through to
// the statement after it.
func (c *checker) climb(owner inspector.Cursor, list []ast.Stmt, i int,
	v types.Object,
) (inspector.Cursor, bool) {
	if _, ok := owner.Node().(*ast.CaseClause); ok {
		// case body -> switch body block -> switch statement
		sw := owner.Parent().Parent()
		switch sw.Node().(type) {
		case *ast.SwitchStmt, *ast.TypeSwitchStmt:
			return sw, true
		}
		return owner, false
	}
	if _, ok := owner.Node().(*ast.CommClause); ok {
		return owner, false
	}
	p := owner.Parent()
	switch n := p.Node().(type) {
	case *ast.BlockStmt:
		return owner, true // a plain { ... } block statement
	case *ast.IfStmt:
		for {
			up := p.Parent()
			if _, ok := up.Node().(*ast.IfStmt); !ok {
				return p, true
			}
			p = up
		}
	case *ast.ForStmt, *ast.RangeStmt:
		for j, s := range list {
			if j != i && (c.mentions(s, v) || leaves(s)) {
				return p, false
			}
		}
		var header []ast.Node
		if f, ok := n.(*ast.ForStmt); ok {
			header = []ast.Node{f.Init, f.Cond, f.Post}
		} else {
			r := n.(*ast.RangeStmt)
			header = []ast.Node{r.Key, r.Value, r.X}
		}
		for _, h := range header {
			if h != nil && c.mentions(h, v) {
				return p, false
			}
		}
		return p, true
	}
	return p, false
}

// stmtList returns the statement list that directly holds the
// statement at cur (or the declaration statement around a ValueSpec),
// its index there, and the node that owns the list.
func stmtList(cur inspector.Cursor) ([]ast.Stmt, int, inspector.Cursor) {
	for {
		if _, ok := cur.Node().(ast.Stmt); ok {
			break
		}
		p := cur.Parent()
		if p.Node() == nil {
			return nil, 0, cur
		}
		cur = p
	}
	owner := cur.Parent()
	var list []ast.Stmt
	switch p := owner.Node().(type) {
	case *ast.BlockStmt:
		list = p.List
	case *ast.CaseClause:
		list = p.Body
	case *ast.CommClause:
		list = p.Body
	default:
		return nil, 0, owner
	}
	for i, s := range list {
		if s == cur.Node() {
			return list, i, owner
		}
	}
	return nil, 0, owner
}

// mentions reports whether n can observe v: it refers to v other than
// in further appends v = append(v, x...) whose x do not mention v.
// Such appends only add elements (other map loops filling the same
// slice) and are covered by the same sort.
func (c *checker) mentions(n ast.Node, v types.Object) bool {
	found := false
	ast.Inspect(n, func(n ast.Node) bool {
		if found {
			return false
		}
		if c.appendOnly(n, v) {
			return false
		}
		if id, ok := n.(*ast.Ident); ok &&
			c.pass.TypesInfo.ObjectOf(id) == v {
			found = true
		}
		return !found
	})
	return found
}

// appendOnly reports whether n is v = append(v, x...) with no x
// mentioning v.
func (c *checker) appendOnly(n ast.Node, v types.Object) bool {
	as, ok := n.(*ast.AssignStmt)
	if !ok || len(as.Lhs) != 1 || len(as.Rhs) != 1 ||
		as.Tok != token.ASSIGN || !c.is(as.Lhs[0], v) {
		return false
	}
	call, ok := ast.Unparen(as.Rhs[0]).(*ast.CallExpr)
	if !ok || len(call.Args) == 0 || !c.is(call.Args[0], v) {
		return false
	}
	fn := c.pass.TypesInfo.Uses[calleeIdent(call)]
	if b, ok := fn.(*types.Builtin); !ok || b.Name() != "append" {
		return false
	}
	for _, x := range call.Args[1:] {
		if c.mentions(x, v) {
			return false
		}
	}
	return true
}

// calleeIdent returns the identifier a call's function expression
// names, or nil.
func calleeIdent(call *ast.CallExpr) *ast.Ident {
	id, _ := ast.Unparen(call.Fun).(*ast.Ident)
	return id
}

// leaves reports whether s may transfer control out of the statement
// list (or into it from elsewhere), so a later statement may not run.
// Function literals are not entered: their returns are their own.
func leaves(s ast.Stmt) bool {
	found := false
	ast.Inspect(s, func(n ast.Node) bool {
		switch n.(type) {
		case *ast.FuncLit:
			return false
		case *ast.ReturnStmt, *ast.BranchStmt, *ast.LabeledStmt:
			found = true
		}
		return !found
	})
	return found
}

// totalSort reports whether call sorts the whole of v (v itself as the
// argument, not v[:n] or a copy) by a total order, so the result does
// not depend on the order v was filled in:
//
//   - sort.Strings, sort.Ints, sort.Float64s, slices.Sort;
//   - sort.Sort or sort.Stable of sort.StringSlice, IntSlice or
//     Float64Slice of v, possibly under sort.Reverse;
//   - slices.SortFunc or SortStableFunc with cmp.Compare or
//     strings.Compare, or a function literal func(a, b T) int whose
//     result is totalCompare;
//   - sort.Slice or SliceStable with func(i, j int) bool returning
//     v[i] < v[j] or v[i] > v[j].
//
// Any other comparator may report distinct elements as equal (sorting
// by one field, by length, or returning 0), which keeps their map
// order, so it is not accepted.
func (c *checker) totalSort(call *ast.CallExpr, v types.Object) bool {
	if len(call.Args) == 0 {
		return false
	}
	switch c.funcName(call) {
	case "sort.Strings", "sort.Ints", "sort.Float64s", "slices.Sort":
		return len(call.Args) == 1 && c.is(call.Args[0], v)
	case "sort.Sort", "sort.Stable":
		return len(call.Args) == 1 && c.sliceType(call.Args[0], v)
	case "slices.SortFunc", "slices.SortStableFunc":
		return len(call.Args) == 2 && c.is(call.Args[0], v) &&
			c.totalCmp(call.Args[1])
	case "sort.Slice", "sort.SliceStable":
		return len(call.Args) == 2 && c.is(call.Args[0], v) &&
			c.totalLess(call.Args[1], v)
	}
	return false
}

// is reports whether e is exactly the variable v.
func (c *checker) is(e ast.Expr, v types.Object) bool {
	id, ok := ast.Unparen(e).(*ast.Ident)
	return ok && c.pass.TypesInfo.ObjectOf(id) == v
}

// sliceType reports whether e is sort.StringSlice(v), IntSlice(v) or
// Float64Slice(v), possibly wrapped in sort.Reverse.
func (c *checker) sliceType(e ast.Expr, v types.Object) bool {
	call, ok := ast.Unparen(e).(*ast.CallExpr)
	if !ok || len(call.Args) != 1 {
		return false
	}
	if c.funcName(call) == "sort.Reverse" {
		return c.sliceType(call.Args[0], v)
	}
	tv, ok := c.pass.TypesInfo.Types[call.Fun]
	if !ok || !tv.IsType() {
		return false
	}
	named, ok := types.Unalias(tv.Type).(*types.Named)
	if !ok || named.Obj().Pkg() == nil ||
		named.Obj().Pkg().Path() != "sort" {
		return false
	}
	switch named.Obj().Name() {
	case "StringSlice", "IntSlice", "Float64Slice":
		return c.is(call.Args[0], v)
	}
	return false
}

// compareFunc reports whether e names cmp.Compare (any instance) or
// strings.Compare.
func (c *checker) compareFunc(e ast.Expr) bool {
	e = ast.Unparen(e)
	if ix, ok := e.(*ast.IndexExpr); ok {
		e = ix.X
	}
	if ix, ok := e.(*ast.IndexListExpr); ok {
		e = ix.X
	}
	var id *ast.Ident
	switch x := e.(type) {
	case *ast.Ident:
		id = x
	case *ast.SelectorExpr:
		id = x.Sel
	default:
		return false
	}
	fn, ok := c.pass.TypesInfo.ObjectOf(id).(*types.Func)
	if !ok || fn.Pkg() == nil {
		return false
	}
	name := fn.Pkg().Path() + "." + fn.Name()
	return name == "cmp.Compare" || name == "strings.Compare"
}

// totalCmp reports whether e is a total three-way comparison:
// cmp.Compare, strings.Compare, or func(a, b T) int { return X } with
// X totalCompare over a and b.
func (c *checker) totalCmp(e ast.Expr) bool {
	if c.compareFunc(e) {
		return true
	}
	lit, ok := ast.Unparen(e).(*ast.FuncLit)
	if !ok {
		return false
	}
	a, b, ok := c.params(lit)
	if !ok || len(lit.Body.List) != 1 {
		return false
	}
	ret, ok := lit.Body.List[0].(*ast.ReturnStmt)
	if !ok || len(ret.Results) != 1 {
		return false
	}
	paths := map[string]bool{}
	if !c.comparePaths(ret.Results[0], a, b, paths) {
		return false
	}
	return covered(a.Type(), "", paths)
}

// params returns the two parameters of a comparator literal.
func (c *checker) params(lit *ast.FuncLit) (a, b types.Object, ok bool) {
	var names []*ast.Ident
	for _, f := range lit.Type.Params.List {
		names = append(names, f.Names...)
	}
	if len(names) != 2 {
		return nil, nil, false
	}
	a = c.pass.TypesInfo.ObjectOf(names[0])
	b = c.pass.TypesInfo.ObjectOf(names[1])
	return a, b, a != nil && b != nil
}

// comparePaths accepts X = Compare(a.p, b.p) (either argument order,
// p a field path, possibly empty) or cmp.Or(X1, ..., Xn) of such, and
// records each compared path p in paths.
func (c *checker) comparePaths(e ast.Expr, a, b types.Object,
	paths map[string]bool,
) bool {
	call, ok := ast.Unparen(e).(*ast.CallExpr)
	if !ok {
		return false
	}
	if c.funcName(call) == "cmp.Or" {
		for _, x := range call.Args {
			if !c.comparePaths(x, a, b, paths) {
				return false
			}
		}
		return len(call.Args) > 0
	}
	if !c.compareFunc(call.Fun) || len(call.Args) != 2 {
		return false
	}
	pa, ra, ok1 := c.path(call.Args[0])
	pb, rb, ok2 := c.path(call.Args[1])
	if !ok1 || !ok2 || pa != pb ||
		!(ra == a && rb == b || ra == b && rb == a) {
		return false
	}
	paths[pa] = true
	return true
}

// path splits a.f.g into the root object a and the path ".f.g".
func (c *checker) path(e ast.Expr) (string, types.Object, bool) {
	switch x := ast.Unparen(e).(type) {
	case *ast.Ident:
		return "", c.pass.TypesInfo.ObjectOf(x), true
	case *ast.SelectorExpr:
		if sel, ok := c.pass.TypesInfo.Selections[x]; !ok ||
			sel.Kind() != types.FieldVal || sel.Indirect() {
			return "", nil, false
		}
		p, root, ok := c.path(x.X)
		return p + "." + x.Sel.Name, root, ok
	}
	return "", nil, false
}

// covered reports whether the compared paths order every value of t
// at prefix totally: the path itself is compared, or t is a struct
// and every field is covered.
func covered(t types.Type, prefix string, paths map[string]bool) bool {
	if paths[prefix] {
		return true
	}
	st, ok := t.Underlying().(*types.Struct)
	if !ok || st.NumFields() == 0 {
		return false
	}
	for f := range st.Fields() {
		if f.Name() == "_" {
			continue
		}
		if !covered(f.Type(), prefix+"."+f.Name(), paths) {
			return false
		}
	}
	return true
}

// totalLess reports whether e is func(i, j int) bool { return v[i] <
// v[j] } (or >, or with i and j swapped): the whole elements, compared
// by an ordered type's own order.
func (c *checker) totalLess(e ast.Expr, v types.Object) bool {
	lit, ok := ast.Unparen(e).(*ast.FuncLit)
	if !ok || len(lit.Body.List) != 1 {
		return false
	}
	i, j, ok := c.params(lit)
	if !ok {
		return false
	}
	ret, ok := lit.Body.List[0].(*ast.ReturnStmt)
	if !ok || len(ret.Results) != 1 {
		return false
	}
	bin, ok := ast.Unparen(ret.Results[0]).(*ast.BinaryExpr)
	if !ok || bin.Op != token.LSS && bin.Op != token.GTR {
		return false
	}
	x, ok1 := c.elem(bin.X, v)
	y, ok2 := c.elem(bin.Y, v)
	return ok1 && ok2 && (x == i && y == j || x == j && y == i)
}

// elem returns k when e is v[k] for an identifier k.
func (c *checker) elem(e ast.Expr, v types.Object) (types.Object, bool) {
	ix, ok := ast.Unparen(e).(*ast.IndexExpr)
	if !ok || !c.is(ix.X, v) {
		return nil, false
	}
	k, ok := ast.Unparen(ix.Index).(*ast.Ident)
	if !ok {
		return nil, false
	}
	return c.pass.TypesInfo.ObjectOf(k), true
}
