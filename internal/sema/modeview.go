package sema

// modeview.go is VHDL-2019 mode views (LRM 6.5.2, #17): view
// declarations, 'CONVERSE, view-typed ports and parameters, and the
// checks for their misuse (an element of mode in assigned, a view port
// associated with an actual that cannot take the drivers the view
// gives). Elaboration reads View to give each element of a view port
// its own direction (#14).

import (
	"strings"

	"github.com/connerohnesorge/gohard/v2/internal/cst"
	"github.com/connerohnesorge/gohard/v2/internal/token"
)

// View is a mode view: the mode of each element of a record type.
// Views are built by the unit that declares them and immutable after;
// a converse is a new View derived from its original on demand.
type View struct {
	// Decl is the view declaration (for a converse, the declaration of
	// the original view).
	Decl *Decl
	// Of is the record subtype the view is of.
	Of *Type
	// Converse marks V'CONVERSE.
	Converse bool
	// Elements are the element mode definitions in record element
	// order; an element the declaration misses (an error) is absent.
	Elements []*ViewElement
}

// ViewElement is the mode of one record element in a view.
type ViewElement struct {
	// Field is the record element.
	Field *Decl
	// Mode is the element's mode: in, out, inout or buffer, or
	// ModeView when the element has a view of its own (View).
	Mode Mode
	View *View
	// Array marks `view (V)`: the element is an array whose elements
	// each have View.
	Array bool
}

// Name returns how the view is written: "v" or "v'converse".
func (v *View) Name() string {
	if v == nil || v.Decl == nil {
		return "the view"
	}
	if v.Converse {
		return v.Decl.Text + "'converse"
	}
	return v.Decl.Text
}

// Element returns the definition of the element named k, or nil.
func (v *View) Element(k Key) *ViewElement {
	if v == nil {
		return nil
	}
	for _, e := range v.Elements {
		if e.Field.Name == k {
			return e
		}
	}
	return nil
}

// ConverseView returns v'CONVERSE (LRM 6.5.2.3): in and out swap,
// buffer becomes in, inout stays, subviews are conversed. Without
// buffer elements the converse of the converse has the original modes.
func (v *View) ConverseView() *View {
	if v == nil {
		return nil
	}
	c := &View{Decl: v.Decl, Of: v.Of, Converse: !v.Converse}
	for _, e := range v.Elements {
		ce := *e
		ce.Mode = converseMode(e.Mode)
		ce.View = e.View.ConverseView()
		c.Elements = append(c.Elements, &ce)
	}
	return c
}

func converseMode(m Mode) Mode {
	switch m {
	case ModeIn:
		return ModeOut
	case ModeOut, ModeBuffer:
		return ModeIn
	}
	return m
}

// writes reports whether an element of mode m is driven by the side
// of the view that has it.
func writes(m Mode) bool {
	return m == ModeOut || m == ModeInout || m == ModeBuffer
}

// leaves calls f with the dotted path and mode of every element of v,
// subviews expanded, in record element order (depth-limited, so a view
// that names itself after an error cannot loop).
func (v *View) leaves(prefix string, depth int, f func(string, Mode)) {
	if v == nil || depth > 16 {
		return
	}
	for _, e := range v.Elements {
		p := prefix + e.Field.Text
		if e.Mode == ModeView {
			v := e.View
			if e.Array {
				p += "(i)"
			}
			v.leaves(p+".", depth+1, f)
			continue
		}
		f(p, e.Mode)
	}
}

func modeText(m Mode) string {
	switch m {
	case ModeIn:
		return "in"
	case ModeOut:
		return "out"
	case ModeInout:
		return "inout"
	case ModeBuffer:
		return "buffer"
	case ModeLinkage:
		return "linkage"
	case ModeView:
		return "view"
	}
	return "none"
}

// simpleMode maps a mode keyword to its Mode.
func simpleMode(k token.Kind) Mode {
	switch k {
	case token.KwIn:
		return ModeIn
	case token.KwOut:
		return ModeOut
	case token.KwInout:
		return ModeInout
	case token.KwBuffer:
		return ModeBuffer
	case token.KwLinkage:
		return ModeLinkage
	}
	return ModeNone
}

// modeViewDecl declares `view V of T is ... end view;` and checks it:
// T is a record type, every element is given exactly one mode, a
// subview is a view of the element's type (LRM 6.5.2.2).
func (a *analyzer) modeViewDecl(mv cst.ModeViewDecl) {
	d := a.newDecl(DeclModeView, mv.Name(), mv.Node)
	si := mv.Subtype()
	t := a.subtypeIndication(si)
	d.Type = t
	v := &View{Decl: d, Of: t}
	d.View = v
	var fields []*Decl
	isRecord := !t.Unknown() && t.Base().Class == TypeRecord
	if isRecord {
		fields = t.Base().Fields
	} else if !t.Unknown() && !a.quiet {
		a.semaError(si.Span(), "a mode view must be of a record type, "+
			"not "+t.Name())
	}
	given := map[Key]*ViewElement{} // lookup only
	for ed := range mv.ElementDefs() {
		if ed.Inactive() {
			continue
		}
		mode, sub, array := a.elementMode(ed.Mode())
		for tok := range ed.Names().Names() {
			k := TokenKey(tok)
			if k == "" {
				continue
			}
			var f *Decl
			for _, fd := range fields {
				if fd.Name == k {
					f = fd
				}
			}
			switch {
			case f == nil:
				if isRecord && !a.quiet {
					a.semaError(tok.Span(), t.Name()+" has no element "+
						tok.Text())
				}
				continue
			case given[k] != nil:
				a.ref(tok, f, RefRead)
				if !a.quiet {
					a.semaError(tok.Span(), "element "+tok.Text()+
						" is given a mode twice in view "+d.Text)
				}
				continue
			}
			a.ref(tok, f, RefRead)
			e := &ViewElement{Field: f, Mode: mode, View: sub,
				Array: array}
			given[k] = e
			if sub != nil {
				a.checkSubview(tok, f, sub, array)
			}
		}
	}
	var missing []string
	for _, f := range fields {
		if e := given[f.Name]; e != nil {
			v.Elements = append(v.Elements, e)
		} else {
			missing = append(missing, f.Text)
		}
	}
	if len(missing) > 0 && !a.quiet {
		what := "element "
		if len(missing) > 1 {
			what = "elements "
		}
		a.semaError(mv.Name().Span(), "view "+d.Text+
			" gives no mode for "+what+strings.Join(missing, ", ")+
			" of "+t.Name())
	}
	a.declare(d)
	a.endClause(mv.End(), d)
}

// elementMode reads an element mode indication: a mode, `view V` or
// `view (V)`.
func (a *analyzer) elementMode(em cst.ElementModeIndication,
) (Mode, *View, bool) {
	if vn := em.View(); !vn.IsZero() {
		array := !em.Node.Token(token.LParen, 0).IsZero()
		return ModeView, a.viewName(vn.Node), array
	}
	tok := em.Node.TokenIn(modeTokens, 0)
	m := simpleMode(tok.Kind())
	if m == ModeLinkage && !a.quiet {
		a.semaError(tok.Span(), "an element of a mode view cannot "+
			"have mode linkage")
	}
	return m, nil, false
}

var modeTokens = token.NewSet(token.KwIn, token.KwOut, token.KwInout,
	token.KwBuffer, token.KwLinkage)

// checkSubview checks that the element f may have the view sub: its
// type is the view's record type, or for `view (V)` an array of it.
func (a *analyzer) checkSubview(tok cst.Token, f *Decl, sub *View,
	array bool,
) {
	ft := f.Type
	if ft.Unknown() || sub.Of.Unknown() || a.quiet {
		return
	}
	if array {
		if ft.Base().Class != TypeArray {
			a.semaError(tok.Span(), "element "+tok.Text()+" is "+
				ft.Name()+", not an array, so it cannot have view ("+
				sub.Name()+")")
			return
		}
		ft = ft.ElemType()
		if ft.Unknown() {
			return
		}
	}
	if !SameBase(ft, sub.Of) {
		a.semaError(tok.Span(), "element "+tok.Text()+" is "+
			ft.Name()+", but view "+sub.Name()+" is of "+sub.Of.Name())
	}
}

// viewName resolves the name of a mode view (a view, an alias of one,
// or V'CONVERSE), reporting a name that denotes something else.
func (a *analyzer) viewName(n cst.Node) *View {
	r := a.resolveName(n, RefType, nil)
	if r.view != nil {
		return r.view
	}
	if !r.unknown && !a.quiet {
		a.semaError(n.Span(), a.text(n)+" is not a mode view")
	}
	return nil
}

// viewIndication analyzes the mode view indication of an interface
// object, `view V [of T]` or `view (V) of T`, and returns the view and
// the object's subtype.
func (a *analyzer) viewIndication(m cst.Node) (*View, *Type) {
	var vn cst.Name
	var si cst.SubtypeIndication
	array := m.Kind() == cst.KindArrayModeViewIndication
	if array {
		mv := cst.ArrayModeViewIndication{Node: m}
		vn, si = mv.View(), mv.Subtype()
	} else {
		mv := cst.ModeViewIndication{Node: m}
		vn, si = mv.View(), mv.Subtype()
	}
	v := a.viewName(vn.Node)
	if si.IsZero() {
		if v == nil || array {
			return v, UnknownType
		}
		return v, orUnknown(v.Of)
	}
	t := a.subtypeIndication(si)
	if v == nil || t.Unknown() || v.Of.Unknown() || a.quiet {
		return v, t
	}
	et := t
	if array {
		if t.Base().Class != TypeArray {
			a.semaError(si.Span(), t.Name()+" is not an array type, "+
				"so it cannot have view ("+v.Name()+")")
			return v, t
		}
		et = t.ElemType()
	}
	if !et.Unknown() && !SameBase(et, v.Of) {
		a.semaError(si.Span(), et.Name()+" is not compatible with "+
			"view "+v.Name()+", which is of "+v.Of.Name())
	}
	return v, t
}

// converse resolves P'CONVERSE.
func (a *analyzer) converse(an cst.AttributeName, p named) named {
	if p.view != nil {
		return named{view: p.view.ConverseView(), tok: an.Designator()}
	}
	if !p.unknown && !a.quiet {
		a.semaError(an.Prefix().Span(), "the prefix of 'converse must "+
			"be a mode view, not "+a.text(an.Prefix().Node))
	}
	return unknownNamed()
}

// viewPath is where a name expression lands in a view port: the port
// (or view parameter), the dotted element path, and the view or mode
// reached. v is set when the path ends on a (sub)view; otherwise mode
// is the element's simple mode.
type viewPath struct {
	port *Decl
	path string
	v    *View
	mode Mode
}

// viewRoot follows e (selections, indexes, slices, object aliases)
// down to its object; ok is false when that object is not a view port
// or parameter, or the path leaves the view's elements.
func viewRoot(e *Expr) (viewPath, bool) {
	var fields []*Decl
	for range 64 {
		if e == nil {
			return viewPath{}, false
		}
		switch e.Kind {
		case ExprSelect:
			fields = append(fields, e.Decl)
			e = e.Prefix
			continue
		case ExprIndex, ExprSlice:
			e = e.Prefix
			continue
		case ExprName:
			if e.Decl != nil && e.Decl.Kind == DeclAlias &&
				e.Decl.Value != nil {
				e = e.Decl.Value
				continue
			}
		}
		break
	}
	if e == nil || e.Kind != ExprName || e.Decl == nil {
		return viewPath{}, false
	}
	d := e.Decl.Denotes()
	if d.Mode != ModeView || d.View == nil {
		return viewPath{}, false
	}
	vp := viewPath{port: d, path: d.Text, v: d.View}
	for i := len(fields) - 1; i >= 0; i-- {
		f := fields[i]
		if f == nil {
			return viewPath{}, false
		}
		vp.path += "." + f.Text
		if vp.v == nil {
			continue // inside an element of a simple mode
		}
		el := vp.v.Element(f.Name)
		if el == nil {
			return viewPath{}, false
		}
		if el.Mode == ModeView {
			vp.v = el.View
			if vp.v == nil {
				return viewPath{}, false
			}
			continue
		}
		vp.v, vp.mode = nil, el.Mode
	}
	return vp, true
}

// firstIn returns the first element of v of mode in, "" if none.
func firstIn(v *View, prefix string) string {
	in := ""
	v.leaves(prefix, 0, func(p string, m Mode) {
		if in == "" && m == ModeIn {
			in = p
		}
	})
	return in
}

// viewWrite checks the target e of an assignment: an element of a view
// port whose mode is in, or a whole (sub)view with such an element,
// cannot be assigned (LRM 6.5.2).
func (a *analyzer) viewWrite(e *Expr, n cst.Node) {
	if a.quiet {
		return
	}
	vp, ok := viewRoot(e)
	if !ok {
		return
	}
	what := a.portWord(vp.port)
	switch {
	case vp.v == nil && vp.mode == ModeIn:
		a.semaError(n.Span(), "cannot assign to "+vp.path+": it has "+
			"mode in in view "+a.viewOf(vp)+" of "+what+" "+
			vp.port.Text)
	case vp.v != nil:
		if in := firstIn(vp.v, vp.path+"."); in != "" {
			a.semaError(n.Span(), "cannot assign to "+vp.path+": "+
				"element "+in+" has mode in in view "+a.viewOf(vp)+
				" of "+what+" "+vp.port.Text)
		}
	}
}

func (a *analyzer) portWord(d *Decl) string {
	if d.Kind == DeclParameter {
		return "parameter"
	}
	return "port"
}

// viewOf names the view of vp's port.
func (a *analyzer) viewOf(vp viewPath) string {
	return vp.port.View.Name()
}

// viewActual checks the actual e of the port f in a port map. A view
// formal drives the elements its view gives mode out, inout or buffer,
// so the actual must not be (an element of) a port of mode in there;
// a formal of mode out, inout or buffer must not be associated with an
// element of mode in of a view port.
func (a *analyzer) viewActual(f *Decl, e *Expr, n cst.Node) {
	if a.quiet || f == nil || e == nil || f.Kind != DeclPort {
		return
	}
	if f.Mode != ModeView || f.View == nil {
		if !writes(f.Mode) {
			return
		}
		vp, ok := viewRoot(e)
		if !ok {
			return
		}
		if vp.v == nil && vp.mode == ModeIn {
			a.semaError(n.Span(), "port "+f.Text+" has mode "+
				modeText(f.Mode)+", but its actual "+vp.path+
				" has mode in in view "+a.viewOf(vp))
		} else if vp.v != nil {
			if in := firstIn(vp.v, vp.path+"."); in != "" {
				a.semaError(n.Span(), "port "+f.Text+" has mode "+
					modeText(f.Mode)+", but element "+in+
					" of its actual has mode in in view "+a.viewOf(vp))
			}
		}
		return
	}
	// The formal has a view: compare it with the actual's modes.
	actualMode := func(string) Mode { return ModeNone }
	desc := ""
	if vp, ok := viewRoot(e); ok {
		if vp.v == nil {
			m := vp.mode
			actualMode = func(string) Mode { return m }
		} else {
			modes := map[string]Mode{} // lookup only
			vp.v.leaves("", 0, func(p string, m Mode) { modes[p] = m })
			actualMode = func(p string) Mode { return modes[p] }
		}
		desc = "in view " + a.viewOf(vp) + " of the actual " + vp.path
	} else if r := actualObject(e); r != nil && r.Kind == DeclPort &&
		r.Mode == ModeIn {
		actualMode = func(string) Mode { return ModeIn }
		desc = "in the actual " + r.Text + ", a port of mode in"
	}
	if desc == "" {
		return
	}
	bad := ""
	var fm Mode
	f.View.leaves("", 0, func(p string, m Mode) {
		if bad == "" && writes(m) && actualMode(p) == ModeIn {
			bad, fm = p, m
		}
	})
	if bad != "" {
		a.semaError(n.Span(), "view "+f.View.Name()+" of port "+f.Text+
			" gives element "+bad+" mode "+modeText(fm)+", but it has "+
			"mode in "+desc)
	}
}

// actualObject returns the object a name expression is part of.
func actualObject(e *Expr) *Decl {
	for range 64 {
		if e == nil {
			return nil
		}
		switch e.Kind {
		case ExprSelect, ExprIndex, ExprSlice:
			e = e.Prefix
			continue
		case ExprName:
			if e.Decl == nil {
				return nil
			}
			if e.Decl.Kind == DeclAlias && e.Decl.Value != nil {
				e = e.Decl.Value
				continue
			}
			return e.Decl.Denotes()
		}
		return nil
	}
	return nil
}

// fnSimple reports whether a formal part names the whole formal: a
// positional association or a simple name.
func fnSimple(fn cst.Node) bool {
	return fn.IsZero() || fn.Kind() == cst.KindSimpleName
}
