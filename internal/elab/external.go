package elab

import (
	"errors"
	"strings"

	"github.com/connerohnesorge/gohard/internal/cst"
	"github.com/connerohnesorge/gohard/internal/elab/hier"
	"github.com/connerohnesorge/gohard/internal/sema"
	"github.com/connerohnesorge/gohard/internal/sim"
	"github.com/connerohnesorge/gohard/internal/token"
)

// External names (LRM 8.7) resolve at elaboration through the
// hierarchy: an absolute pathname starts at the top (.tb.dut.x), a
// relative one at the innermost concurrent region of the name (the
// instance, block or generate of the process), each ^. going up one
// region, and a package pathname (@lib.pkg.x) names a package object,
// which sema already resolved. A pathname element is a statement label
// (instance, block or generate), a for-generate element carries its
// index (lanes(2)); an if or case generate's alternative is implicit.

// External is a resolved external name.
type External struct {
	// Class is the class the name was declared with.
	Class sema.ObjClass
	// Inst is the instance that declares the object (nil for a
	// package object) and Decl its declaration.
	Inst *hier.Instance
	Decl *sema.Decl
	// Sig is the signal of a signal external name.
	Sig sim.SigID
}

// ErrExternal is wrapped by every external-name resolution error.
var ErrExternal = errors.New("external name")

// extError is one external-name error: a single line naming the path,
// matching ErrExternal under errors.Is.
type extError struct{ msg string }

func (e *extError) Error() string        { return e.msg }
func (e *extError) Is(target error) bool { return target == ErrExternal }

func extErr(path, why string) error {
	return &extError{"external name " + path + ": " + why}
}

// External resolves the external name e (an sema.Expr of an external
// name node) used in instance in. A signal must already be elaborated:
// a name that reaches a signal declared later is an error, like an
// object that does not exist or has another class.
func (d *Design) External(in *hier.Instance, e *sema.Expr) (External,
	error,
) {
	en, ok := cst.As[cst.ExternalName](e.Node)
	if !ok {
		return External{}, extErr(e.Node.Text(), "not an external name")
	}
	class := sema.ClassConstant
	switch en.Class().Kind() {
	case token.KwSignal:
		class = sema.ClassSignal
	case token.KwVariable:
		class = sema.ClassVariable
	}
	path := en.Path().Node
	text := strings.Join(strings.Fields(path.Text()), "")
	if path.Kind() == cst.KindPackagePathname {
		if e.Decl == nil {
			return External{}, extErr(text, "no such package object")
		}
		if e.Decl.Class != class && !(class == sema.ClassConstant &&
			e.Decl.Kind == sema.DeclConstant) {
			return External{}, extErr(text, "the object is a "+
				e.Decl.Kind.String())
		}
		return External{Class: class, Decl: e.Decl}, nil
	}
	cur := in
	var elems []cst.PathnameElement
	var object string
	for el := range path.Elements() {
		switch {
		case el.IsToken() && el.Token.Kind() == token.Caret:
			cur = d.Parent(cur)
			if cur == nil {
				return External{}, extErr(text, "^ goes above the top")
			}
		case el.IsToken() && (el.Token.Kind() == token.Identifier ||
			el.Token.Kind() == token.ExtendedIdentifier):
			object = string(sema.TokenKey(el.Token))
		case !el.IsToken():
			if pe, ok := cst.As[cst.PathnameElement](el.Node); ok {
				elems = append(elems, pe)
			}
		}
	}
	if object == "" && len(elems) > 0 {
		// The object name parsed as a final element.
		last := elems[len(elems)-1]
		elems = elems[:len(elems)-1]
		object = string(sema.TokenKey(last.Name()))
	}
	if path.Kind() == cst.KindAbsolutePathname {
		cur = d.Top
		if len(elems) == 0 || !strings.EqualFold(
			string(sema.TokenKey(elems[0].Name())), d.Top.Path) {
			return External{}, extErr(text,
				"an absolute pathname starts at "+d.Top.Path)
		}
		elems = elems[1:]
	}
	for _, pe := range elems {
		next := d.child(cur, pe)
		if next == nil {
			return External{}, extErr(text, "no "+
				strings.TrimSuffix(strings.TrimSpace(pe.Node.Text()),
					".")+" in "+cur.Path)
		}
		cur = next
	}
	return d.object(cur, object, class, text)
}

// child returns the child of cur labelled by pe (with pe's generate
// index for a for generate).
func (d *Design) child(cur *hier.Instance, pe cst.PathnameElement,
) *hier.Instance {
	label := sema.TokenKey(pe.Name())
	idx := ""
	if g := pe.GenerateIndex(); !g.IsZero() {
		idx = strings.ToLower(strings.Join(strings.Fields(g.Node.Text()),
			""))
	}
	for _, c := range cur.Children {
		if c.Stmt == nil || c.Stmt.Label == nil ||
			c.Stmt.Label.Name != label {
			continue
		}
		if idx != "" && strings.ToLower(c.Param.Image()) != idx {
			continue
		}
		return c
	}
	return nil
}

// object finds the object name declared directly in instance in.
func (d *Design) object(in *hier.Instance, name string,
	class sema.ObjClass, text string,
) (External, error) {
	key := sema.Key(name)
	var regions []*sema.Region
	switch in.Kind {
	case hier.KindEntity:
		regions = append(regions, in.Entity.Region)
		if in.Arch != nil {
			regions = append(regions, in.Arch.Region)
		}
	case hier.KindBlock:
		regions = append(regions, in.Stmt.Region)
	case hier.KindGenerate:
		if in.Alt != nil {
			regions = append(regions, in.Alt.Region)
		} else {
			regions = append(regions, in.Stmt.Region)
		}
	}
	for _, r := range regions {
		if r == nil {
			continue
		}
		for _, x := range r.Decls {
			if x.Name != key {
				continue
			}
			got := x.Class
			switch x.Kind {
			case sema.DeclConstant, sema.DeclGeneric:
				got = sema.ClassConstant
			case sema.DeclSignal:
				got = sema.ClassSignal
			case sema.DeclSharedVariable, sema.DeclVariable:
				got = sema.ClassVariable
			}
			if got != class {
				return External{}, extErr(text, "the object is a "+
					x.Kind.String())
			}
			ext := External{Class: class, Inst: in, Decl: x}
			if class == sema.ClassSignal {
				id, ok := d.sigs[sigKey{in, x}]
				if !ok {
					return External{}, extErr(text,
						"the signal is not elaborated yet")
				}
				ext.Sig = id
			}
			return ext, nil
		}
	}
	return External{}, extErr(text, "no object "+name+" in "+in.Path)
}
