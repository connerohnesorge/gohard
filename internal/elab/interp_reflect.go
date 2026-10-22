package elab

// interp_reflect.go is std.reflection (VHDL-2019, LRM 16.7), built
// natively: the package has no body, its protected types are mirrors
// GoHard makes in Go.
//
// X'REFLECT (reflectValue) takes a value mirror of object X's current
// value, or a subtype mirror of subtype X. A mirror is a node (mnode:
// a value of a subtype, or a subtype) seen through one of two faces:
// the generic VALUE_MIRROR_PT or SUBTYPE_MIRROR_PT, or the face of its
// type class (INTEGER_VALUE_MIRROR_PT, ...). Every face is a protected
// object of the world: a handle from world.handle, designated by an
// access value on the heap, so deallocation and equality work as for
// any access value. Nodes are immutable; what a mirror leads to (its
// subtype, elements, bounds, the other face) is made on first use,
// charged to the step budget (P7), and kept in the world so asking
// twice gives the same mirror. Handles are never printed.
//
// Open details follow NVC 1.23 (#18): simple names are the upper-case
// name of the nearest named subtype (an anonymous subtype gives its
// base type's), record elements and enumeration literals count from
// 0, physical units from 1, an access mirror's designated value is
// taken when the access value mirror is made. Array elements are
// indexed by their index values (NVC 1.23 gets descending ranges
// wrong).

import (
	"errors"
	"maps"
	"strconv"
	"strings"

	"github.com/connerohnesorge/gohard/internal/eval"
	"github.com/connerohnesorge/gohard/internal/sema"
)

// The TYPE_CLASS positions (std.reflection).
const (
	classEnum = iota
	classInteger
	classFloating
	classPhysical
	classRecord
	classArray
	classAccess
	classFile
	classProtected
)

// classNames are the TYPE_CLASS literals by position, without CLASS_.
var classNames = [...]string{"enumeration", "integer", "floating",
	"physical", "record", "array", "access", "file", "protected"}

// classOf is the TYPE_CLASS position of layout t, or -1.
func classOf(t *eval.Type) int {
	if t == nil {
		return -1
	}
	switch t.Kind {
	case eval.KindEnum:
		return classEnum
	case eval.KindInteger:
		return classInteger
	case eval.KindFloating:
		return classFloating
	case eval.KindPhysical:
		return classPhysical
	case eval.KindRecord:
		return classRecord
	case eval.KindArray:
		return classArray
	case eval.KindAccess:
		return classAccess
	case eval.KindFile:
		return classFile
	case eval.KindProtected:
		return classProtected
	}
	return -1
}

// mnode is what a mirror reflects: a value (sub false) of subtype
// st/et, or the subtype st/et itself. It never changes.
type mnode struct {
	sub   bool
	class int
	st    *sema.Type  // the sema subtype (nil when unknown)
	et    *eval.Type  // its layout
	v     eval.Value  // the value (value mirrors)
	deref *eval.Value // an access value's designated value (nil: null)
	// fname and fmode are a file object's logical name and
	// FILE_OPEN_KIND position, when the mirror was made ("", 0 when
	// not open).
	fname string
	fmode int64
}

// mface is one mirror object: a node and the face it shows (generic:
// VALUE_MIRROR_PT or SUBTYPE_MIRROR_PT).
type mface struct {
	n       *mnode
	generic bool
}

// mlink names something a node leads to: its subtype, the other face,
// element i, a bound.
type mlink struct {
	n    *mnode
	what linkKind
	i    int64
}

type linkKind uint8

const (
	linkSubtype linkKind = iota // the subtype of a value
	linkElem                    // element or field i
	linkBound                   // bound i of a scalar subtype (left..high)
	linkLiteral                 // enumeration literal i
	linkIndex                   // index subtype of dimension i
	linkElemSub                 // element subtype (array) or field i's
	linkDeref                   // the designated value / subtype
)

// faceKey names a face of a node.
type faceKey struct {
	n       *mnode
	generic bool
}

// mirrorState is the world's mirrors; fork copies it.
type mirrorState struct {
	// objs maps the handle of each mirror object to its face.
	objs map[int64]mface
	// faces are the access values of the faces made so far, links the
	// nodes they lead to.
	faces map[faceKey]eval.Value
	links map[mlink]*mnode
}

func newMirrorState() mirrorState {
	return mirrorState{objs: map[int64]mface{},
		faces: map[faceKey]eval.Value{}, links: map[mlink]*mnode{}}
}

func (s mirrorState) clone() mirrorState {
	return mirrorState{objs: maps.Clone(s.objs),
		faces: maps.Clone(s.faces), links: maps.Clone(s.links)}
}

// reflectValue lowers the attribute name e, X'REFLECT, to the mirror
// it gives now: for an object X a VALUE_MIRROR of its current value,
// for a subtype X a SUBTYPE_MIRROR. machine.lowerAttr calls it for
// e.Attr == "reflect"; ok is always true.
func (m *machine) reflectValue(ev *sema.Evaluator, e *sema.Expr,
	b *eval.Budget,
) (eval.Expr, bool, error) {
	if m.b == nil {
		m.b = b
	}
	p := e.Prefix
	if p == nil || p.Type == nil || p.Type.Unknown() {
		return nil, true, errors.New("'REFLECT of an unknown prefix")
	}
	et, err := ev.LayoutIn(p.Type, b)
	if err != nil {
		return nil, true, err
	}
	var n *mnode
	if p.Kind == sema.ExprTypeMark {
		if et.Kind == eval.KindArray && !et.Constrained() {
			return nil, true, errors.New("the prefix of 'REFLECT must " +
				"be a fully constrained subtype")
		}
		n = &mnode{sub: true, st: p.Type, et: et}
	} else {
		v, err := ev.Eval(p, b)
		if err != nil {
			return nil, true, err
		}
		if n, err = m.valueNode(p.Type, et, v); err != nil {
			return nil, true, err
		}
	}
	n.class = classOf(n.et)
	acc, err := m.face(n, true)
	if err != nil {
		return nil, true, err
	}
	return &eval.Const{V: acc}, true, nil
}

// valueNode makes the node of value v of subtype st (layout et): an
// array subtype gets the value's bounds, an access value its
// designated value, a file its name and mode.
func (m *machine) valueNode(st *sema.Type, et *eval.Type, v eval.Value,
) (*mnode, error) {
	if et == nil {
		et = v.T
	}
	if et != nil && et.Kind == eval.KindArray && len(v.B) > 0 &&
		(!et.Constrained() || len(et.Bounds) != len(v.B)) {
		et = et.ArraySubtype(v.B...)
	}
	n := &mnode{st: st, et: et, v: v, class: classOf(et)}
	switch n.class {
	case classAccess:
		if v.I != 0 {
			d, err := m.w.deref(v)
			if err != nil {
				return nil, err
			}
			n.deref = &d
		}
	case classFile:
		if f, ok := m.w.files[v.I]; ok && f.open {
			n.fname, n.fmode = f.name, f.mode
		}
	}
	return n, m.b.Step(1)
}

// face returns the access value of face generic of node n, making the
// mirror object on first use.
func (m *machine) face(n *mnode, generic bool) (eval.Value, error) {
	ms := &m.w.mirrors
	k := faceKey{n, generic}
	if v, ok := ms.faces[k]; ok {
		return v, nil
	}
	if err := m.b.Step(1); err != nil {
		return eval.Value{}, err
	}
	name := classNames[n.class] + "_"
	if generic {
		name = ""
	}
	if n.sub {
		name += "subtype_mirror"
	} else {
		name += "value_mirror"
	}
	name = strings.ToUpper(name)
	h := m.w.handle()
	ms.objs[h] = mface{n: n, generic: generic}
	pt := &eval.Type{Kind: eval.KindProtected, Name: name + "_PT"}
	at := &eval.Type{Kind: eval.KindAccess, Name: name}
	v := m.w.alloc(at, eval.Value{T: pt, I: h})
	ms.faces[k] = v
	return v, nil
}

// link returns what n leads to under l, making it with mk on first
// use.
func (m *machine) link(l mlink, mk func() (*mnode, error)) (*mnode,
	error,
) {
	ms := &m.w.mirrors
	if c, ok := ms.links[l]; ok {
		return c, nil
	}
	c, err := mk()
	if err != nil {
		return nil, err
	}
	if c.class = classOf(c.et); c.class < 0 {
		return nil, errors.New("std.reflection: a subtype of no class")
	}
	if err := m.b.Step(1); err != nil {
		return nil, err
	}
	ms.links[l] = c
	return c, nil
}

// subtypeOf is the subtype node of value node n.
func (m *machine) subtypeOf(n *mnode) (*mnode, error) {
	if n.sub {
		return n, nil
	}
	return m.link(mlink{n, linkSubtype, 0}, func() (*mnode, error) {
		return &mnode{sub: true, st: n.st, et: n.et}, nil
	})
}

// subtypeNode is the node of subtype st with layout et (et nil: laid
// out by the running evaluator).
func (m *machine) subtypeNode(st *sema.Type, et *eval.Type) (*mnode,
	error,
) {
	if et == nil {
		if st == nil || st.Unknown() {
			return nil, errors.New("std.reflection: an unknown subtype")
		}
		var err error
		if et, err = m.ev.LayoutIn(st, m.b); err != nil {
			return nil, err
		}
	}
	return &mnode{sub: true, st: st, et: et}, nil
}

// reflectFunc evaluates a std.reflection function (a method of obj).
func (m *machine) reflectFunc(x *sema.Decl, obj int64, args []arg,
	rt *eval.Type,
) (eval.Value, error) {
	if m.w == nil {
		return eval.Value{}, unsupported(x)
	}
	f, ok := m.w.mirrors.objs[obj]
	if !ok {
		// Not a mirror GoHard made (an object of a mirror type the
		// design declared itself): no native.
		return eval.Value{}, unsupported(x)
	}
	n := f.n
	name := string(x.Name)
	// Methods every face of its kind has.
	switch name {
	case "get_value_class", "get_type_class":
		return eval.Enum(rt, int64(n.class)), nil
	case "simple_name":
		return strValue(rt, simpleName(n)), nil
	case "to_value_mirror", "to_subtype_mirror":
		return m.face(n, true)
	case "get_subtype_mirror":
		s, err := m.subtypeOf(n)
		if err != nil {
			return eval.Value{}, err
		}
		return m.face(s, f.generic)
	}
	if f.generic {
		if c, ok := strings.CutPrefix(name, "to_"); ok {
			if classNames[n.class] != c {
				return eval.Value{}, errors.New("the " + kindWord(n) +
					" mirror is of class CLASS_" +
					strings.ToUpper(classNames[n.class]) + ", not CLASS_" +
					strings.ToUpper(c))
			}
			return m.face(n, false)
		}
		return eval.Value{}, unsupported(x)
	}
	var (
		v   eval.Value
		err error
	)
	if n.sub {
		v, err = m.subtypeMethod(x, n, args, rt)
	} else {
		v, err = m.valueMethod(x, n, args, rt)
	}
	if err != nil {
		return eval.Value{}, err
	}
	if rt != nil && rt.Kind != eval.KindAccess && rt.Kind != eval.KindArray {
		return eval.Check(v, rt)
	}
	return v, nil
}

// kindWord is "value" or "subtype".
func kindWord(n *mnode) string {
	if n.sub {
		return "subtype"
	}
	return "value"
}

// reflectProc executes a std.reflection procedure: there is none.
func (m *machine) reflectProc(x *sema.Decl, obj int64, args []arg,
) error {
	_, _ = obj, args
	return unsupported(x)
}

// simpleName is the upper-case name of n's subtype.
func simpleName(n *mnode) string {
	if n.st != nil && !n.st.Unknown() {
		if s := n.st.Name(); !strings.HasPrefix(s, "anonymous ") {
			return strings.ToUpper(s)
		}
	}
	if n.et != nil {
		return strings.ToUpper(n.et.Name)
	}
	return ""
}

// argString is argument i as a string, and whether it is one.
func argString(args []arg, i int) (string, bool) {
	if i >= len(args) || args[i].v.Kind() != eval.KindArray ||
		classOf(args[i].v.T.BaseType().Elem) != classEnum {
		return "", false
	}
	return stringOf(args[i].v), true
}

// valueMethod is a method of a class-specific value mirror.
func (m *machine) valueMethod(x *sema.Decl, n *mnode, args []arg,
	rt *eval.Type,
) (eval.Value, error) {
	v := n.v
	switch string(x.Name) {
	case "value":
		if n.class == classFloating {
			return eval.RealValue(rt, v.F), nil
		}
		return eval.Int(rt, v.I), nil
	case "pos":
		return eval.Int(rt, v.I), nil
	case "image":
		return strValue(rt, mirrorImage(v)), nil
	case "unit_index":
		return eval.Int(rt, 1), nil // the image's unit: the primary
	case "is_null":
		return eval.Bool(n.deref == nil), nil
	case "get":
		return m.valueGet(n, args)
	case "get_file_logical_name":
		return strValue(rt, n.fname), nil
	case "get_file_open_kind":
		return eval.Enum(rt, openKindPos(rt, n.fmode)), nil
	}
	return eval.Value{}, unsupported(x)
}

// openKindPos is the position in FILE_OPEN_KIND (layout t) of file
// mode mode: the inverse of openKind.
func openKindPos(t *eval.Type, mode int64) int64 {
	name := [...]string{modeRead: "read_mode", modeWrite: "write_mode",
		modeAppend: "append_mode", modeReadWrite: "read_write_mode"}
	if t == nil || mode < 0 || mode >= int64(len(name)) {
		return mode
	}
	for i, l := range t.BaseType().Literals {
		if strings.EqualFold(l, name[mode]) {
			return int64(i)
		}
	}
	return mode
}

// mirrorImage is the image a value mirror gives: 'IMAGE, with a
// physical value's unit in upper case as in NVC.
func mirrorImage(v eval.Value) string {
	if v.Kind() == eval.KindPhysical {
		if u := v.T.BaseType().Units; len(u) > 0 {
			return strconv.FormatInt(v.I, 10) + " " +
				strings.ToUpper(u[0].Name)
		}
	}
	return v.Image()
}

// valueGet is GET of a record, array or access value mirror: the
// VALUE_MIRROR of an element, field or the designated object.
func (m *machine) valueGet(n *mnode, args []arg) (eval.Value, error) {
	var (
		i   int64
		mk  func() (*mnode, error)
		err error
	)
	switch n.class {
	case classAccess:
		if n.deref == nil {
			return eval.Value{}, &eval.Error{Code: eval.ErrCodeNull,
				Msg: "GET of the value mirror of a null access value"}
		}
		mk = func() (*mnode, error) {
			var st *sema.Type
			if n.st != nil && !n.st.Unknown() {
				st = n.st.Base().Designated
			}
			return m.valueNode(st, n.deref.T, *n.deref)
		}
		c, err := m.link(mlink{n, linkDeref, 0}, mk)
		if err != nil {
			return eval.Value{}, err
		}
		return m.face(c, true)
	case classRecord:
		if i, err = m.fieldIndex(n, args); err != nil {
			return eval.Value{}, err
		}
		mk = func() (*mnode, error) {
			return m.valueNode(fieldSub(n.st, int(i)),
				n.et.Fields[i].Type, n.v.E[i])
		}
	case classArray:
		if i, err = elemOffset(n, args); err != nil {
			return eval.Value{}, err
		}
		mk = func() (*mnode, error) {
			return m.valueNode(elemSub(n.st), n.et.BaseType().Elem,
				n.v.E[i])
		}
	default:
		return eval.Value{}, errors.New("GET of a " +
			classNames[n.class] + " value mirror")
	}
	c, err := m.link(mlink{n, linkElem, i}, mk)
	if err != nil {
		return eval.Value{}, err
	}
	return m.face(c, true)
}

// fieldSub is the sema subtype of field i of record subtype st.
func fieldSub(st *sema.Type, i int) *sema.Type {
	if st == nil || st.Unknown() {
		return nil
	}
	if i < len(st.FieldSubs) && st.FieldSubs[i] != nil {
		return st.FieldSubs[i]
	}
	if fs := st.Base().Fields; i < len(fs) {
		return fs[i].Type
	}
	return nil
}

// elemSub is the sema element subtype of array subtype st.
func elemSub(st *sema.Type) *sema.Type {
	if st == nil || st.Unknown() {
		return nil
	}
	if st.ElemSub != nil {
		return st.ElemSub
	}
	return st.Base().Elem
}

// indexSub is the sema index subtype of dimension d of array type st.
func indexSub(st *sema.Type, d int) *sema.Type {
	if st == nil || st.Unknown() {
		return nil
	}
	if ix := st.Base().Index; d < len(ix) {
		return ix[d]
	}
	return nil
}

// fieldIndex is the element of a record mirror that args[0] names: a
// 0-based index or an element name (any case).
func (m *machine) fieldIndex(n *mnode, args []arg) (int64, error) {
	fs := n.et.BaseType().Fields
	if len(fs) == 0 {
		fs = n.et.Fields
	}
	if s, ok := argString(args, 0); ok {
		for i, f := range fs {
			if strings.EqualFold(f.Name, s) {
				return int64(i), nil
			}
		}
		return 0, errors.New(s + " is not an element of record type " +
			simpleName(n))
	}
	if len(args) == 0 {
		return 0, errors.New("GET of a record mirror without an index")
	}
	i := args[0].v.I
	if i < 0 || i >= int64(len(fs)) {
		return 0, eval.IndexError(i, eval.Range{Left: 0,
			Right: int64(len(fs)) - 1, Dir: eval.To})
	}
	return i, nil
}

// elemOffset is the row-major position of the element of an array
// value mirror that args index: one index per dimension, or one
// INDEX_VECTOR.
func elemOffset(n *mnode, args []arg) (int64, error) {
	var idx []int64
	if len(args) == 1 && args[0].v.Kind() == eval.KindArray {
		for _, e := range args[0].v.E {
			idx = append(idx, e.I)
		}
	} else {
		for _, a := range args {
			idx = append(idx, a.v.I)
		}
	}
	bs := n.v.B
	if len(bs) == 0 {
		bs = n.et.Bounds
	}
	if len(idx) != len(bs) {
		return 0, errors.New("GET with " + strconv.Itoa(len(idx)) +
			" indices of a mirror of a " + strconv.Itoa(len(bs)) +
			"-dimensional array")
	}
	var off int64
	for d, r := range bs {
		if !r.Contains(idx[d]) {
			return 0, eval.IndexError(idx[d], r)
		}
		off = off*r.Len() + r.Offset(idx[d])
	}
	if off < 0 || off >= int64(len(n.v.E)) {
		return 0, errors.New("GET of an element the value does not have")
	}
	return off, nil
}

// subtypeMethod is a method of a class-specific subtype mirror.
func (m *machine) subtypeMethod(x *sema.Decl, n *mnode, args []arg,
	rt *eval.Type,
) (eval.Value, error) {
	et := n.et
	name := string(x.Name)
	switch n.class {
	case classArray:
		return m.arrayMethod(x, n, args, rt)
	case classRecord:
		return m.recordMethod(x, n, args, rt)
	case classAccess, classFile:
		if name != "designated_subtype" {
			break
		}
		c, err := m.link(mlink{n, linkDeref, 0}, func() (*mnode, error) {
			var st *sema.Type
			if n.st != nil && !n.st.Unknown() {
				st = n.st.Base().Designated
				if n.class == classFile {
					st = n.st.Base().Content
				}
			}
			return m.subtypeNode(st, nil)
		})
		if err != nil {
			return eval.Value{}, err
		}
		return m.face(c, true)
	case classPhysical:
		switch name {
		case "units_length":
			return eval.Int(rt, int64(len(et.BaseType().Units))), nil
		case "unit_name", "unit_index", "scale":
			u, err := physUnit(n, args)
			if err != nil {
				return eval.Value{}, err
			}
			us := et.BaseType().Units
			switch name {
			case "unit_name":
				return strValue(rt, strings.ToUpper(us[u].Name)), nil
			case "unit_index":
				return eval.Int(rt, int64(u)+1), nil
			}
			return eval.Int(rt, us[u].Factor), nil
		}
	case classEnum:
		if name == "enumeration_literal" {
			i, err := literalOf(n, args)
			if err != nil {
				return eval.Value{}, err
			}
			c, err := m.link(mlink{n, linkLiteral, i},
				func() (*mnode, error) {
					return &mnode{st: n.st, et: et,
						v: eval.Enum(et, i)}, nil
				})
			if err != nil {
				return eval.Value{}, err
			}
			return m.face(c, false)
		}
	}
	return m.scalarMethod(x, n, rt)
}

// scalarMethod is a bound, LENGTH or ASCENDING of a scalar subtype
// mirror.
func (m *machine) scalarMethod(x *sema.Decl, n *mnode, rt *eval.Type,
) (eval.Value, error) {
	et := n.et
	name := string(x.Name)
	bounds := map[string]int64{"left": 0, "right": 1, "low": 2,
		"high": 3}
	if n.class == classFloating {
		r := et.Real
		switch name {
		case "ascending":
			return eval.Bool(r.Dir == eval.To), nil
		}
		b, ok := bounds[name]
		if !ok {
			return eval.Value{}, unsupported(x)
		}
		f := [...]float64{r.Left, r.Right, r.Low(), r.High()}[b]
		return m.boundMirror(n, b, eval.RealValue(et, f))
	}
	r := et.Range
	switch name {
	case "ascending":
		return eval.Bool(r.Dir == eval.To), nil
	case "length":
		l, err := r.CheckedLen()
		if err != nil {
			return eval.Value{}, err
		}
		return eval.Int(rt, l), nil
	}
	b, ok := bounds[name]
	if !ok {
		return eval.Value{}, unsupported(x)
	}
	i := [...]int64{r.Left, r.Right, r.Low(), r.High()}[b]
	return m.boundMirror(n, b, eval.Value{T: et, I: i})
}

// boundMirror is the class-specific value mirror of bound b (left,
// right, low, high) of scalar subtype n, of value v.
func (m *machine) boundMirror(n *mnode, b int64, v eval.Value,
) (eval.Value, error) {
	c, err := m.link(mlink{n, linkBound, b}, func() (*mnode, error) {
		return &mnode{st: n.st, et: n.et, v: v}, nil
	})
	if err != nil {
		return eval.Value{}, err
	}
	return m.face(c, false)
}

// physUnit is the 0-based unit of a physical subtype mirror that
// args[0] names: a 1-based index or a unit name (any case).
func physUnit(n *mnode, args []arg) (int, error) {
	us := n.et.BaseType().Units
	if s, ok := argString(args, 0); ok {
		for i, u := range us {
			if strings.EqualFold(u.Name, s) {
				return i, nil
			}
		}
		return 0, errors.New(s + " is not a unit of physical type " +
			simpleName(n))
	}
	i := args[0].v.I
	if i < 1 || i > int64(len(us)) {
		return 0, eval.IndexError(i, eval.Range{Left: 1,
			Right: int64(len(us)), Dir: eval.To})
	}
	return int(i - 1), nil
}

// literalOf is the position of the enumeration literal args[0] names:
// a position, or the literal's image (identifiers in any case,
// character literals with their quotes).
func literalOf(n *mnode, args []arg) (int64, error) {
	lits := n.et.BaseType().Literals
	if s, ok := argString(args, 0); ok {
		for i, l := range lits {
			if l == s || l[0] != '\'' && strings.EqualFold(l, s) {
				return int64(i), nil
			}
		}
		return 0, errors.New(s + " does not denote an enumeration " +
			"literal of type " + simpleName(n))
	}
	i := args[0].v.I
	if i < 0 || i >= int64(len(lits)) {
		return 0, eval.IndexError(i, eval.Range{Left: 0,
			Right: int64(len(lits)) - 1, Dir: eval.To})
	}
	return i, nil
}

// arrayMethod is a method of an ARRAY_SUBTYPE_MIRROR.
func (m *machine) arrayMethod(x *sema.Decl, n *mnode, args []arg,
	rt *eval.Type,
) (eval.Value, error) {
	et := n.et
	bt := et.BaseType()
	switch string(x.Name) {
	case "dimensions":
		return eval.Int(rt, int64(len(bt.Index))), nil
	case "element_subtype":
		c, err := m.link(mlink{n, linkElemSub, 0}, func() (*mnode, error) {
			return m.subtypeNode(elemSub(n.st), et.Elem)
		})
		if err != nil {
			return eval.Value{}, err
		}
		return m.face(c, true)
	}
	d := int64(1)
	if len(args) > 0 {
		d = args[0].v.I
	}
	if d < 1 || d > int64(len(bt.Index)) {
		return eval.Value{}, eval.IndexError(d, eval.Range{Left: 1,
			Right: int64(len(bt.Index)), Dir: eval.To})
	}
	if x.Name == "index_subtype" {
		c, err := m.link(mlink{n, linkIndex, d}, func() (*mnode, error) {
			return m.subtypeNode(indexSub(n.st, int(d-1)), bt.Index[d-1])
		})
		if err != nil {
			return eval.Value{}, err
		}
		return m.face(c, true)
	}
	if int(d) > len(et.Bounds) {
		return eval.Value{}, errors.New("the bounds of an unconstrained " +
			"array subtype")
	}
	r := et.Bounds[d-1]
	switch x.Name {
	case "left":
		return eval.Int(rt, r.Left), nil
	case "right":
		return eval.Int(rt, r.Right), nil
	case "low":
		return eval.Int(rt, r.Low()), nil
	case "high":
		return eval.Int(rt, r.High()), nil
	case "length":
		l, err := r.CheckedLen()
		return eval.Int(rt, l), err
	case "ascending":
		return eval.Bool(r.Dir == eval.To), nil
	}
	return eval.Value{}, unsupported(x)
}

// recordMethod is a method of a RECORD_SUBTYPE_MIRROR.
func (m *machine) recordMethod(x *sema.Decl, n *mnode, args []arg,
	rt *eval.Type,
) (eval.Value, error) {
	fs := n.et.Fields
	switch x.Name {
	case "length":
		return eval.Int(rt, int64(len(fs))), nil
	case "element_name", "element_index", "element_subtype":
		i, err := m.fieldIndex(n, args)
		if err != nil {
			return eval.Value{}, err
		}
		switch x.Name {
		case "element_name":
			return strValue(rt, strings.ToUpper(fs[i].Name)), nil
		case "element_index":
			return eval.Int(rt, i), nil
		}
		c, err := m.link(mlink{n, linkElemSub, i}, func() (*mnode, error) {
			return m.subtypeNode(fieldSub(n.st, int(i)), fs[i].Type)
		})
		if err != nil {
			return eval.Value{}, err
		}
		return m.face(c, true)
	}
	return eval.Value{}, unsupported(x)
}
