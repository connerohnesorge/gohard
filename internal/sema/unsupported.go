package sema

// unsupported.go finds opaque units (#20, R3): encrypted IP (IEEE 1735
// `protect envelopes) and legal VHDL that GoHard does not implement,
// reported with the one code unsupported/construct. Opaque units exit
// 2; allow_opaque downgrades encrypted IP only; an allowed unit with a
// simulation.models stand-in is checked through the stand-in (its
// dependents are checked, the downgrade is a hint); units that depend
// on any other opaque unit are unchecked (sema/unchecked-unit), so a run
// with them never exits 0. This file is the extension point for the
// construct table.
//
// Every VHDL-2019 feature of #18 that v1.0 does not implement is in the
// table below (the syntactic ones by node kind) or found by meaning
// (detectors: std.env and std.reflection names, 'REFLECT, composite
// 'IMAGE, 64-bit INTEGER values, inferred constraints, protected-type
// ports, composites of and pointers to protected types, protected and
// access function parameters). A protected type whose body declares
// variables of other protected types is VHDL-2008 (OSVVM relies on it)
// and is analyzed; #18's "protected types built from other protected
// types" are the composites. Each construct is reported once per
// design entity (a primary unit and its secondary units), at its first
// use in canonical order; every unit using it is opaque, and so is every
// secondary unit of a primary unit made opaque by a construct (an
// architecture cannot be analyzed without its entity). Under an older
// standard the 2019 constructs are not "unsupported" but not allowed:
// the parser reports the syntactic ones and this file reports the rest
// that no other check catches as syntax/standard (exit 1).

import (
	"cmp"
	"math"
	"path"
	"slices"
	"strings"

	"github.com/connerohnesorge/gohard/internal/cst"
	"github.com/connerohnesorge/gohard/internal/diag"
	"github.com/connerohnesorge/gohard/internal/eval"
	"github.com/connerohnesorge/gohard/internal/token"
)

// construct is one unimplemented construct, found by node kind.
type construct struct {
	kind cst.Kind
	name string
	// planned marks the v1.1 constructs (#18, #27): VHDL-2019
	// features, unsupported only when the selected standard is 2019
	// (the parser rejects them under older standards).
	planned bool
	// match, when set, must also accept the node.
	match func(n cst.Node) bool
}

// The names of the v1.1 constructs found by meaning rather than by node
// kind. The texts are the ones testdata/corpus/corpus.yaml expects
// (prot_cond expected_v1_0) and #18 lists.
const (
	cReflection      = "std.reflection"
	cEnvVersion      = "std.env VHDL_VERSION and TOOL_* functions"
	cEnvGetenv       = "std.env GETENV"
	cEnvFile         = "std.env FILE_NAME/FILE_PATH/FILE_LINE"
	cEnvAssert       = "std.env assert/PSL counter-and-format API"
	cEnvTime         = "std.env date and time functions"
	cEnvDir          = "std.env DIR_* functions"
	cEnvCallPath     = "std.env GET_CALL_PATH"
	cEnvOther        = "std.env VHDL-2019 additions"
	cInferred        = "inferred constraints"
	cInt64           = "64-bit integers"
	cImage           = "'IMAGE for composite types"
	cPathName        = "'PATH_NAME/'INSTANCE_NAME for protected types"
	cVariablePort    = "shared-variable ports of protected type"
	cProtArray       = "arrays of protected types"
	cProtRecord      = "records of protected types"
	cProtAccess      = "access types to protected types"
	cProtParam       = "protected-type and access parameters on functions"
	cMethodAlias     = "method aliases in protected types"
	cProtGenerics    = "generic clauses on protected types"
	cCondReturn      = "conditional return ... when"
	cCallGenericMap  = "generic maps on subprogram calls"
	cPrivateVariable = "private variable declarations"
)

// constructs are the legal constructs GoHard does not implement in
// v1.0, by node kind.
var constructs = []construct{
	{kind: cst.KindPrivateVariableDecl, name: cPrivateVariable,
		planned: true},
	{kind: cst.KindProtectedTypeHeader, name: cProtGenerics,
		planned: true, match: func(n cst.Node) bool {
			return !(cst.ProtectedTypeHeader{Node: n}).Generics().IsZero()
		}},
	{kind: cst.KindProtectedTypeInstantiationDef, name: cProtGenerics,
		planned: true},
	{kind: cst.KindConditionalReturnStmt, name: cCondReturn,
		planned: true},
	{kind: cst.KindCallGenericMap, name: cCallGenericMap, planned: true},
	{kind: cst.KindParameterMapAspect, name: cCallGenericMap,
		planned: true},
	{kind: cst.KindAliasDecl, name: cMethodAlias, planned: true,
		match: inProtectedDecl},
	// PSL verification units bound in a configuration (`use vunit`):
	// legal VHDL-2008 that neither sema nor the kernel implements.
	{kind: cst.KindVerificationUnitBinding,
		name: "verification unit bindings in configurations"},
}

// legacy lists the v1.1 constructs that an older standard does not
// allow and that no parser or name rule reports there; this file
// reports them as syntax/standard. The other v1.1 constructs are
// syntax the parser gates, or std.env/std.reflection names the
// per-standard views hide (sema/undefined, library/missing-unit).
var legacy = map[string]bool{
	cInferred: true, cVariablePort: true, cProtParam: true,
	cMethodAlias: true, cProtArray: true, cProtRecord: true,
}

// inProtectedDecl reports whether n is inside a protected type
// declaration (not its body).
func inProtectedDecl(n cst.Node) bool {
	for a := range n.Ancestors() {
		switch a.Kind() {
		case cst.KindProtectedTypeDef:
			return true
		case cst.KindProtectedTypeBody, cst.KindSubprogramBody:
			return false
		}
	}
	return false
}

// finding is one use of an unimplemented construct.
type finding struct {
	rg      cst.Range
	name    string
	planned bool
}

// opaqueUnits marks encrypted and unsupported units opaque and their
// dependents unchecked.
func (s *session) opaqueUnits() {
	if s.base == nil {
		return // the bundle is never opaque
	}
	for _, f := range s.files {
		s.encryptedFile(f)
	}
	reported := map[groupKey]map[string]bool{}
	byConstruct := map[*Unit]bool{}
	for _, u := range s.units {
		s.encrypted(u)
		if s.unsupported(u, reported) {
			byConstruct[u] = true
		}
	}
	// A secondary unit of a primary unit made opaque by a construct is
	// opaque too: its declarations extend the primary unit's.
	for _, u := range s.units {
		if u.Opaque || u.Kind.Primary() {
			continue
		}
		if p := s.primaryOf(u); p != nil && byConstruct[p] {
			u.Opaque = true
		}
	}
	for _, u := range s.units {
		if u.Opaque {
			u.diags = keepOpaque(u.diags)
		}
	}
	standIns := s.standInOriginals()
	bound := s.boundEntities()
	for _, u := range s.units {
		if u.Opaque {
			continue
		}
		if o := opaqueDep(u, standIns, bound); o != nil {
			u.Unchecked = true
			// Its semantic errors may only be consequences of the
			// opaque unit it reads (a name the unit declares that
			// GoHard could not see): the unit is reported as not
			// checked instead. Syntax and library errors stay.
			u.diags = dropSema(u.diags)
			u.diags = append(u.diags, at(u.File, unitRange(u),
				diag.New(diag.SemaUnchecked, u.Label(), o.Label())))
		}
	}
}

// groupKey names a design entity: a primary unit with its secondary
// units.
type groupKey struct{ lib, name Key }

func groupOf(u *Unit) groupKey {
	if u.Kind.Primary() {
		return groupKey{u.Library, u.Name}
	}
	return groupKey{u.Library, u.Of}
}

// primaryOf returns the visible primary unit of secondary unit u.
func (s *session) primaryOf(u *Unit) *Unit {
	l, ok := s.libs[u.Library]
	if !ok {
		return nil
	}
	p, ok := l.UnitFrom(u.Of, u)
	if !ok {
		return nil
	}
	return p
}

func unitRange(u *Unit) cst.Range {
	if u.Decl != nil {
		return u.Decl.Span
	}
	return u.Lib.Span()
}

// keepOpaque drops the semantic diagnostics of an opaque unit: GoHard
// cannot analyze it, so they would be guesses. The opacity diagnostic
// stays.
func keepOpaque(ds []diag.Diagnostic) []diag.Diagnostic {
	var out []diag.Diagnostic
	for _, d := range ds {
		switch d.Code.Area() {
		case "sema", "library":
			if d.Code != diag.SemaEncryptedUnit &&
				d.Code != diag.SemaOpaqueAllowed {
				continue
			}
		}
		out = append(out, d)
	}
	return out
}

// dropSema removes the sema diagnostics of an unchecked unit.
func dropSema(ds []diag.Diagnostic) []diag.Diagnostic {
	var out []diag.Diagnostic
	for _, d := range ds {
		if d.Code.Area() != "sema" {
			out = append(out, d)
		}
	}
	return out
}

// opaqueDep returns the first opaque unit u depends on, transitively,
// in canonical order of the search. An allowed encrypted unit with a
// stand-in model does not make its dependents unchecked; a stand-in
// whose encrypted original is not allowed counts as the original
// (except for the stand-in's own units, which are plain VHDL).
func opaqueDep(u *Unit, standIns map[*Unit]*Unit,
	bound map[*Unit][]*Unit) *Unit {
	seen := map[*Unit]bool{u: true}
	queue := []*Unit{u}
	for len(queue) > 0 {
		x := queue[0]
		queue = queue[1:]
		for _, d := range slices.Concat(x.Deps, bound[x]) {
			if seen[d] {
				continue
			}
			seen[d] = true
			if d.Opaque && !d.Modeled {
				return d
			}
			if o := standIns[d]; o != nil && o.Opaque && !o.Modeled &&
				d.File != u.File {
				return o
			}
			queue = append(queue, d)
		}
	}
	return nil
}

// boundEntities returns, per unit, the entities its component
// instantiations bind to by default (LRM 7.3.3: the entity named like
// the component in the unit's working library), sorted by Index. A
// component bound explicitly names its entity, which is a Dep already.
func (s *session) boundEntities() map[*Unit][]*Unit {
	out := map[*Unit][]*Unit{}
	for _, u := range s.units {
		l, ok := s.libs[u.Library]
		if !ok {
			continue
		}
		var es []*Unit
		var walk func([]*Stmt)
		walk = func(ss []*Stmt) {
			for _, st := range ss {
				if st.Kind == StmtInstance && st.Component != nil {
					e, ok := l.Unit(st.Component.Name)
					if ok && e != u && e.Kind == UnitEntity &&
						!slices.Contains(es, e) {
						es = append(es, e)
					}
				}
				walk(st.Body)
				for _, a := range st.Alts {
					walk(a.Body)
				}
			}
		}
		walk(u.Body)
		slices.SortFunc(es, func(a, b *Unit) int {
			return cmp.Compare(a.Index, b.Index)
		})
		if len(es) > 0 {
			out[u] = es
		}
	}
	return out
}

// encryptedFile handles an encrypted envelope between design units
// (before a unit's first token or at the end of the file). The units
// inside it are unknown; by the vendor convention of a plain entity
// followed by its encrypted architecture, the envelope belongs to the
// design unit before it, which becomes opaque. An envelope before any
// unit names the file.
func (s *session) encryptedFile(f *File) {
	var toks []cst.Token
	for du := range (cst.DesignFile{Node: f.Root()}).Units() {
		toks = append(toks, du.FirstToken())
	}
	toks = append(toks, cst.DesignFile{Node: f.Root()}.Eof())
	for i, t := range toks {
		for _, tr := range t.Leading() {
			if tr.Kind != token.ProtectedData {
				continue
			}
			var prev *Unit
			for _, u := range f.Units {
				if u.Node.Span().End <= t.FullSpan().Start {
					prev = u
				}
			}
			if prev == nil || i == 0 {
				s.diags = append(s.diags, at(f, t.FullSpan(),
					diag.New(diag.SemaEncryptedUnit,
						"an encrypted block in "+f.Path)))
			} else {
				s.markEncrypted(prev)
			}
			return
		}
	}
}

// markEncrypted marks u opaque as encrypted IP, or reports the
// allow_opaque downgrade (naming the stand-in when there is one).
func (s *session) markEncrypted(u *Unit) {
	if u.Opaque {
		return
	}
	u.Opaque = true
	rg := unitRange(u)
	if allowed(s.cfg.AllowOpaque, u) {
		dg := diag.New(diag.SemaOpaqueAllowed, u.Label())
		if st := s.standIn(u); st != nil {
			u.Modeled = true
			dg = dg.WithRelated(standInNote(u, st))
		}
		u.diags = append(u.diags, at(u.File, rg, dg))
		return
	}
	u.diags = append(u.diags, at(u.File, rg,
		diag.New(diag.SemaEncryptedUnit, u.Label())))
}

// standInNote is the related location of an allow_opaque hint: the
// stand-in that dependents are checked against.
func standInNote(u, st *Unit) diag.Related {
	r := diag.Related{Path: pathOf(st), Message: "units using " +
		u.Label() + " are checked against its stand-in in " +
		pathOf(st) + " (simulation.models)"}
	if st.File != nil {
		rg := unitRange(st)
		r.Line, r.Col = st.File.Lines.LineCol(rg.Start)
	}
	return r
}

// modelFor returns the simulation.models path for u (lib.unit names u
// or, for a secondary unit, its primary unit).
func (s *session) modelFor(u *Unit) (string, bool) {
	for _, m := range s.cfg.Simulation.Models {
		lib, unit, ok := strings.Cut(m.Unit, ".")
		if ok && IdentKey(lib) == u.Library &&
			(IdentKey(unit) == u.Name || IdentKey(unit) == u.Of) {
			return cleanSlash(m.Path), true
		}
	}
	return "", false
}

func cleanSlash(p string) string {
	return path.Clean(strings.ReplaceAll(p, `\`, "/"))
}

// standIn returns the stand-in unit simulation.models gives u: the
// primary unit of the same name in the model file. Its dependents are
// checked against the stand-in's ports (#20); a model listed without
// such a unit is no stand-in.
func (s *session) standIn(u *Unit) *Unit {
	p, ok := s.modelFor(u)
	if !ok {
		return nil
	}
	name := u.Name
	if !u.Kind.Primary() {
		name = u.Of
	}
	for _, f := range s.files {
		if f.Path != p {
			continue
		}
		for _, m := range f.Units {
			if m.Kind.Primary() && m.Name == name &&
				m.Library == u.Library {
				return m
			}
		}
	}
	return nil
}

// standInOriginals maps each stand-in unit that replaced an encrypted
// original in its library (libs.go, applyStandIns) to that original.
func (s *session) standInOriginals() map[*Unit]*Unit {
	out := map[*Unit]*Unit{}
	for _, u := range s.units {
		if !u.Duplicate || !u.Kind.Primary() {
			continue
		}
		if st := s.standIn(u); st != nil && st != u && !st.Duplicate {
			out[st] = u
		}
	}
	return out
}

// encrypted marks u opaque when an encrypted envelope is inside it.
func (s *session) encrypted(u *Unit) {
	first := true
	for t := range u.Node.DescendantTokens() {
		lead := t.Leading()
		if first {
			first, lead = false, nil // encryptedFile's
		}
		for _, tr := range lead {
			if tr.Kind != token.ProtectedData {
				continue
			}
			s.markEncrypted(u)
			return
		}
	}
}

// allowed reports whether allow_opaque lists u as lib.unit.
func allowed(list []string, u *Unit) bool {
	for _, e := range list {
		lib, unit, ok := strings.Cut(e, ".")
		if ok && IdentKey(lib) == u.Library &&
			(IdentKey(unit) == u.Name || IdentKey(unit) == u.Of) {
			return true
		}
	}
	return false
}

// unsupported reports the unimplemented constructs u uses, each once
// per design entity (reported tracks what was reported), and marks u
// opaque when it uses one. It returns whether it did. Under a standard
// older than 2019 the v1.1 constructs in legacy are reported as
// syntax/standard instead and do not make u opaque.
func (s *session) unsupported(u *Unit,
	reported map[groupKey]map[string]bool,
) bool {
	fs := s.findings(u)
	slices.SortStableFunc(fs, func(a, b finding) int {
		return cmp.Or(cmp.Compare(a.rg.Start, b.rg.Start),
			cmp.Compare(a.rg.End, b.rg.End), strings.Compare(a.name,
				b.name))
	})
	g := groupOf(u)
	if reported[g] == nil {
		reported[g] = map[string]bool{}
	}
	opaque := false
	for _, f := range fs {
		if f.planned && s.std < token.Std2019 {
			if legacy[f.name] {
				u.diags = append(u.diags, at(u.File, f.rg,
					diag.New(diag.SyntaxStandard, f.name,
						token.Std2019.String(), s.std.String())))
			}
			continue
		}
		opaque = true
		if reported[g][f.name] {
			continue
		}
		reported[g][f.name] = true
		v := diag.PleaseReport
		if f.planned {
			v = diag.PlannedV11
		}
		u.diags = append(u.diags, at(u.File, f.rg,
			diag.NewVariant(diag.UnsupportedConstruct, v, f.name)))
	}
	if opaque {
		u.Opaque = true
	}
	return opaque
}

// findings lists every use of an unimplemented construct in u, in no
// particular order.
func (s *session) findings(u *Unit) []finding {
	var out []finding
	add := func(rg cst.Range, name string) {
		out = append(out, finding{rg: rg, name: name, planned: true})
	}
	// Walk the tree with an explicit stack, pruning inactive branches
	// on the way down: asking Inactive per node walks the ancestors,
	// which is quadratic on a long operator chain (a left-nested tree
	// as deep as it has terms), and so would recursion depth be.
	var stack []cst.Node
	if !u.Node.Inactive() {
		stack = append(stack, u.Node)
	}
	for len(stack) > 0 {
		n := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if n.Kind() == cst.KindInactiveBranch {
			continue
		}
		for c := range n.Nodes() {
			stack = append(stack, c)
		}
		for _, c := range constructs {
			if n.Kind() == c.kind && (c.match == nil || c.match(n)) {
				out = append(out, finding{rg: n.Span(), name: c.name,
					planned: c.planned})
			}
		}
		if n.Kind() == cst.KindAttributeName {
			s.attrFindings(u, cst.AttributeName{Node: n}, add)
		}
	}
	for _, ref := range u.Refs {
		if name := bundledConstruct(refDecl(ref)); name != "" {
			add(ref.Span, name)
		}
	}
	for _, d := range u.Decls {
		if d.File != u.File || d.Implicit {
			continue
		}
		declFindings(d, add)
	}
	for _, d := range u.Decls {
		if d.File == u.File && !d.Implicit && d.Value != nil &&
			d.Kind.IsObject() {
			int64Findings(d.Value, d.Type, add)
		}
	}
	for _, e := range u.Exprs {
		int64Findings(e, nil, add)
	}
	return out
}

// refDecl returns what a reference denotes, through aliases.
func refDecl(ref Ref) *Decl {
	d := ref.Decl
	if d == nil && ref.Overload != nil {
		d = ref.Overload.Chosen
	}
	return d.Denotes()
}

// bundledConstruct names the v1.1 construct a reference to the bundled
// declaration d uses: anything of std.reflection, and the VHDL-2019
// additions to std.env.
func bundledConstruct(d *Decl) string {
	if d == nil || d.Unit == nil || d.Unit.File == nil ||
		!d.Unit.File.Bundled || d.Unit.Library != "std" {
		return ""
	}
	switch d.Unit.Name {
	case "reflection":
		return cReflection
	case "env":
	default:
		return ""
	}
	first := d.First
	if first < token.Std2019 && d.Type != nil && d.Type.Base() != nil &&
		d.Type.Base().Decl != nil && d.Kind == DeclEnumLiteral {
		first = d.Type.Base().Decl.First
	}
	if first < token.Std2019 {
		return ""
	}
	return envGroup(d)
}

// envGroup sorts a VHDL-2019 std.env declaration into the #18 groups:
// by its name, else (implicit operations, to_string, "+") by the type
// of its first parameter, else (record elements, enumeration literals)
// by its type or record.
func envGroup(d *Decl) string {
	g := envName(string(d.Name))
	if g == "" && len(d.Params) > 0 && d.Params[0].Type != nil {
		g = envName(d.Params[0].Type.Name())
	}
	if g == "" && d.Kind == DeclElement && d.Parent != nil &&
		d.Parent.Owner != nil {
		g = envName(string(d.Parent.Owner.Name))
	}
	if g == "" && d.Type != nil && d.Type.Base().Decl != nil {
		g = envName(string(d.Type.Base().Decl.Name))
	}
	if g == "" {
		return cEnvOther
	}
	return g
}

// envName is the #18 group of a std.env name, or "".
func envName(name string) string {
	n := strings.ToUpper(name)
	switch {
	case n == "VHDL_VERSION" || strings.HasPrefix(n, "TOOL_"):
		return cEnvVersion
	case n == "GETENV":
		return cEnvGetenv
	case n == "FILE_NAME" || n == "FILE_PATH" || n == "FILE_LINE":
		return cEnvFile
	case strings.Contains(n, "CALL_PATH"):
		return cEnvCallPath
	case strings.HasPrefix(n, "DIR") || n == "FILE_DELETE_STATUS":
		return cEnvDir
	case strings.Contains(n, "ASSERT") || strings.Contains(n, "PSL") ||
		strings.Contains(n, "READSEVERITY"):
		return cEnvAssert
	case strings.Contains(n, "TIME_") || strings.HasSuffix(n, "TIME") ||
		n == "EPOCH" || n == "DAYOFWEEK" || n == "REAL":
		return cEnvTime
	}
	return ""
}

// attrFindings reports 'REFLECT, 'IMAGE of a composite type and
// 'PATH_NAME/'INSTANCE_NAME of a name inside a protected type.
func (s *session) attrFindings(u *Unit, an cst.AttributeName,
	add func(cst.Range, string),
) {
	des := an.Designator()
	if des.IsZero() || des.Missing() {
		return
	}
	k := TokenKey(des)
	switch k {
	case "reflect":
		add(des.Span(), cReflection)
		return
	case "image", "path_name", "instance_name":
	default:
		return
	}
	pre := an.Prefix()
	if pre.IsZero() {
		return
	}
	last := pre.LastToken()
	if last.IsZero() {
		return
	}
	ref, ok := refAt(u.Refs, last.Span().Start)
	if !ok {
		return
	}
	d := refDecl(ref)
	if d == nil {
		return
	}
	if k == "image" {
		if (d.Kind.IsTypeMark() || d.Kind.IsObject()) && d.Type != nil {
			switch d.Type.Base().Class {
			case TypeArray, TypeRecord:
				add(an.Span(), cImage)
			}
		}
		return
	}
	if d.Parent != nil && (d.Parent.Kind == RegionProtected ||
		d.Parent.Kind == RegionProtectedBody) {
		add(an.Span(), cPathName)
	}
}

// declFindings reports the declaration-shaped v1.1 constructs of d.
func declFindings(d *Decl, add func(cst.Range, string)) {
	switch d.Kind {
	case DeclPort:
		if d.Class == ClassVariable {
			add(d.Span, cVariablePort)
		}
	case DeclVariable, DeclSignal:
		if d.Value != nil && unconstrained(d.Type, 0) {
			add(d.Span, cInferred)
		}
	case DeclType:
		t := d.Type
		if t == nil {
			return
		}
		// A constrained array type declaration declares a subtype of an
		// anonymous base type: the element is the base's.
		t = t.Base()
		switch t.Class {
		case TypeArray:
			if protected(t.Elem) {
				add(d.Span, cProtArray)
			}
		case TypeRecord:
			for _, f := range t.Fields {
				if protected(f.Type) {
					add(d.Span, cProtRecord)
					break
				}
			}
		case TypeAccess:
			if protected(t.Designated) {
				add(d.Span, cProtAccess)
			}
		}
	case DeclFunction:
		for _, p := range d.Params {
			if p.Type == nil {
				continue
			}
			switch p.Type.Base().Class {
			case TypeProtected, TypeAccess:
				add(p.Span, cProtParam)
			}
		}
	}
}

func protected(t *Type) bool {
	return t != nil && t.Base() != nil && t.Base().Class == TypeProtected
}

// unconstrained reports whether an array subtype leaves an index range
// open (at any element level): an object of it with an initial value
// takes its constraint from the value (VHDL-2019 inferred constraints).
func unconstrained(t *Type, depth int) bool {
	if t == nil || depth > 8 || t.Base() == nil ||
		t.Base().Class != TypeArray || t.isDynamic() {
		return false
	}
	cs := t.Constraints()
	if cs == nil || slices.Contains(cs, nil) {
		return true
	}
	return unconstrained(t.ElemType(), depth+1)
}

// int64Findings reports the outermost INTEGER expressions of e whose
// static value does not fit 32 bits: VHDL-2019 makes INTEGER 64 bits,
// which v1.0 does not implement (INTEGER is 32 bits in every view). ctx
// is the subtype the context expects (nil when unknown), so a
// universal_integer expression converted to INTEGER counts as INTEGER;
// in other contexts (the range of a user-defined integer type) a large
// universal value is legal VHDL-2008 and is analyzed.
func int64Findings(e *Expr, ctx *Type, add func(cst.Range, string)) {
	if e == nil {
		return
	}
	if isStdInteger(e.Type) || (isStdInteger(ctx) && universalInt(e.Type)) {
		if v, ok := foldInt(e, 0); ok && (v > math.MaxInt32 ||
			v < math.MinInt32) {
			add(e.Node.Span(), cInt64)
			return
		}
	}
	int64Findings(e.Prefix, nil, add)
	for i, a := range e.Args {
		var at *Type
		if i < len(e.Formals) && e.Formals[i] != nil {
			at = e.Formals[i].Type
		}
		int64Findings(a, at, add)
	}
	for _, as := range e.Assocs {
		for _, c := range as.Choices {
			int64Findings(c, nil, add)
		}
		int64Findings(as.Value, nil, add)
	}
	if r := e.Range; r != nil {
		int64Findings(r.Left, nil, add)
		int64Findings(r.Right, nil, add)
	}
}

func universalInt(t *Type) bool {
	b := t.Base()
	return b != nil && b.Universal && b.Class == TypeInteger
}

// isStdInteger reports whether t is a subtype of std.standard.INTEGER.
func isStdInteger(t *Type) bool {
	if t == nil {
		return false
	}
	b := t.Base()
	if b == nil || b.Class != TypeInteger || b.Universal || b.Decl == nil {
		return false
	}
	u := b.Decl.Unit
	return b.Decl.Name == "integer" && u != nil && u.Library == "std" &&
		u.Name == "standard"
}

// foldInt computes the value of an integer expression made of literals
// and the predefined arithmetic operators, in 64 bits.
func foldInt(e *Expr, depth int) (int64, bool) {
	if e == nil || depth > 64 {
		return 0, false
	}
	switch e.Kind {
	case ExprLiteral:
		if e.Lit.Kind() == token.IntegerLiteral {
			return ParseInteger(e.Lit.Text())
		}
	case ExprQualified, ExprConversion:
		if len(e.Args) == 1 {
			return foldInt(e.Args[0], depth+1)
		}
	case ExprCall:
		x := e.Decl.Denotes()
		if x == nil || !x.Implicit || x.Op == 0 {
			return 0, false
		}
		var vs []int64
		for _, a := range e.Args {
			v, ok := foldInt(a, depth+1)
			if !ok {
				return 0, false
			}
			vs = append(vs, v)
		}
		return applyInt(eval.Op(x.Op), vs)
	}
	return 0, false
}

func applyInt(o eval.Op, vs []int64) (int64, bool) {
	if len(vs) == 1 {
		x := vs[0]
		switch o {
		case eval.OpPlus:
			return x, true
		case eval.OpNeg:
			return -x, x != math.MinInt64
		case eval.OpAbs:
			return max(x, -x), x != math.MinInt64
		}
		return 0, false
	}
	if len(vs) != 2 {
		return 0, false
	}
	x, y := vs[0], vs[1]
	switch o {
	case eval.OpAdd:
		r := x + y
		return r, (x >= 0) != (y >= 0) || (r >= 0) == (x >= 0)
	case eval.OpSub:
		r := x - y
		return r, (x >= 0) == (y >= 0) || (r >= 0) == (x >= 0)
	case eval.OpMul:
		return mulInt(x, y)
	case eval.OpDiv:
		if y == 0 || (x == math.MinInt64 && y == -1) {
			return 0, false
		}
		return x / y, true
	case eval.OpPow:
		if y < 0 {
			return 0, false
		}
		r := int64(1)
		for range min(y, 64) {
			var ok bool
			if r, ok = mulInt(r, x); !ok {
				return 0, false
			}
		}
		if y > 64 && x != 0 && x != 1 && x != -1 {
			return 0, false
		}
		return r, true
	}
	return 0, false
}

// mulInt multiplies in 64 bits; false on overflow.
func mulInt(x, y int64) (int64, bool) {
	if x == 0 || y == 0 {
		return 0, true
	}
	r := x * y
	if r/y != x || (x == -1 && y == math.MinInt64) ||
		(y == -1 && x == math.MinInt64) {
		return 0, false
	}
	return r, true
}
