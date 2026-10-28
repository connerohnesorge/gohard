package stdlib

import (
	"strings"

	"github.com/connerohnesorge/gohard/v2/internal/cst"
	"github.com/connerohnesorge/gohard/v2/internal/parser"
	"github.com/connerohnesorge/gohard/v2/internal/token"
)

// Decl is one declaration a bundled package makes visible: the package
// itself, or an item of its declarative part. Enumeration literals,
// physical units, record elements and protected-type methods belong to
// their type's Decl; implicitly declared operations are not listed
// (analysis declares them, #34).
type Decl struct {
	// Library and Unit are lower-case: "ieee", "numeric_std".
	Library string
	Unit    string
	// Kind is the declaration's kind: package, type, incomplete type,
	// subtype, constant, signal, variable, shared variable, file,
	// alias, attribute, component, function, procedure.
	Kind string
	// Name is the declared name: identifiers upper-case, operator
	// symbols lower-case in quotes, character literals as written.
	Name string
	// Signature is the subprogram signature "[T1, T2 return R]" of a
	// function, procedure or subprogram alias (type marks upper-case,
	// as written), else "".
	Signature string
	// Path is the bundled file and Span the declared name in it.
	Path string
	Span cst.Range
	// First is the first standard that has the declaration (#33).
	First token.Standard
}

// Key is the declaration's identity in firststd.tsv:
// `ieee.numeric_std function "+" [UNRESOLVED_UNSIGNED,
// UNRESOLVED_UNSIGNED return UNRESOLVED_UNSIGNED]`. Keys are unique.
func (d Decl) Key() string {
	k := d.Library + "." + d.Unit + " " + d.Kind + " " + d.Name
	if d.Signature != "" {
		k += " " + d.Signature
	}
	return k
}

// declsOf returns the declarations of the package declared in src,
// parsed as VHDL-2019 (the bundled text is 2019 text in every view), in
// source order, with First unset. A package body or a file without a
// package gives none.
func declsOf(lib, file string, src []byte, std token.Standard) []Decl {
	r := parser.Parse(src, parser.Options{Standard: std})
	var out []Decl
	for du := range r.Tree().Nodes() {
		for u := range du.Nodes() {
			out = append(out, unitDecls(lib, file, u)...)
		}
	}
	return out
}

func unitDecls(lib, file string, u cst.Node) []Decl {
	base := Decl{Library: lib, Path: file}
	switch u.Kind() {
	case cst.KindPackageDecl:
		p, _ := cst.As[cst.PackageDecl](u)
		base.Unit = strings.ToLower(p.Name().Text())
		pkg := base
		pkg.Kind, pkg.Name, pkg.Span = "package", upper(p.Name()),
			p.Name().Span()
		out := []Decl{pkg}
		for d := range p.Decls().Nodes() {
			out = append(out, itemDecls(base, d)...)
		}
		return out
	case cst.KindPackageInstantiation:
		p, _ := cst.As[cst.PackageInstantiation](u)
		base.Unit = strings.ToLower(p.Name().Text())
		base.Kind, base.Name, base.Span = "package", upper(p.Name()),
			p.Name().Span()
		return []Decl{base}
	}
	return nil
}

// itemDecls returns the declarations one declarative item makes.
func itemDecls(base Decl, n cst.Node) []Decl {
	one := func(kind string, name cst.Token) []Decl {
		d := base
		d.Kind, d.Name, d.Span = kind, designator(name), name.Span()
		return []Decl{d}
	}
	many := func(kind string, names cst.IdentifierList) []Decl {
		var out []Decl
		for t := range names.Names() {
			out = append(out, one(kind, t)...)
		}
		return out
	}
	switch n.Kind() {
	case cst.KindTypeDecl:
		d, _ := cst.As[cst.TypeDecl](n)
		return one("type", d.Name())
	case cst.KindIncompleteTypeDecl:
		d, _ := cst.As[cst.IncompleteTypeDecl](n)
		return one("incomplete type", d.Name())
	case cst.KindSubtypeDecl:
		d, _ := cst.As[cst.SubtypeDecl](n)
		return one("subtype", d.Name())
	case cst.KindConstantDecl:
		d, _ := cst.As[cst.ConstantDecl](n)
		return many("constant", d.Names())
	case cst.KindSignalDecl:
		d, _ := cst.As[cst.SignalDecl](n)
		return many("signal", d.Names())
	case cst.KindVariableDecl:
		d, _ := cst.As[cst.VariableDecl](n)
		kind := "variable"
		if !d.Shared().IsZero() {
			kind = "shared variable"
		}
		return many(kind, d.Names())
	case cst.KindFileDecl:
		d, _ := cst.As[cst.FileDecl](n)
		return many("file", d.Names())
	case cst.KindAttributeDecl:
		d, _ := cst.As[cst.AttributeDecl](n)
		return one("attribute", d.Name())
	case cst.KindComponentDecl:
		d, _ := cst.As[cst.ComponentDecl](n)
		return one("component", d.Name())
	case cst.KindAliasDecl:
		d, _ := cst.As[cst.AliasDecl](n)
		out := one("alias", d.Designator())
		out[0].Signature = signature(d.Signature())
		return out
	case cst.KindSubprogramDecl:
		d, _ := cst.As[cst.SubprogramDecl](n)
		return subprogram(base, d.Spec().Node)
	case cst.KindSubprogramInstantiation:
		d, _ := cst.As[cst.SubprogramInstantiation](n)
		kind := "function"
		if !d.Token(token.KwProcedure, 0).IsZero() {
			kind = "procedure"
		}
		out := one(kind, d.Designator())
		out[0].Signature = signature(d.Signature())
		return out
	case cst.KindPackageDecl, cst.KindPackageInstantiation:
		return unitDecls(base.Library, base.Path, n)
	}
	return nil // use clauses, attribute specifications, ...
}

// subprogram returns the Decl of a function or procedure specification.
func subprogram(base Decl, spec cst.Node) []Decl {
	var (
		name   cst.Token
		params cst.ParameterList
		ret    cst.Name
		kind   = "procedure"
	)
	if f, ok := cst.As[cst.FunctionSpec](spec); ok {
		name, params, ret, kind = f.Designator(), f.Params(),
			f.ReturnType().TypeMark(), "function"
	} else if p, ok := cst.As[cst.ProcedureSpec](spec); ok {
		name, params = p.Designator(), p.Params()
	} else {
		return nil
	}
	var marks []string
	for it := range params.List().Items() {
		o, ok := cst.As[cst.InterfaceObjectDecl](it.Node)
		if !ok {
			continue
		}
		m := nameText(o.Subtype().TypeMark().Node)
		for range o.Names().Names() {
			marks = append(marks, m)
		}
	}
	d := base
	d.Kind, d.Name, d.Span = kind, designator(name), name.Span()
	d.Signature = formatSignature(marks, nameText(ret.Node))
	return []Decl{d}
}

// signature renders a Signature node in the Decl.Signature form; a
// missing signature gives "".
func signature(s cst.Signature) string {
	if s.IsZero() {
		return ""
	}
	var marks []string
	ret := ""
	afterReturn := false
	for e := range s.Elements() {
		if e.IsToken() {
			afterReturn = afterReturn || e.Token.Kind() == token.KwReturn
			continue
		}
		n := e.Node
		if afterReturn {
			ret = nameText(n)
		} else {
			marks = append(marks, nameText(n))
		}
	}
	return formatSignature(marks, ret)
}

func formatSignature(marks []string, ret string) string {
	s := "[" + strings.Join(marks, ", ")
	if ret != "" {
		if len(marks) > 0 {
			s += " "
		}
		s += "return " + ret
	}
	return s + "]"
}

// nameText is a name's tokens without trivia, identifiers upper-case.
func nameText(n cst.Node) string {
	if n.IsZero() {
		return ""
	}
	var b strings.Builder
	for t := range n.DescendantTokens() {
		b.WriteString(designator(t))
	}
	return b.String()
}

// designator spells a declared name: identifiers and keywords
// upper-case, operator symbols lower-case, character literals and
// extended identifiers as written.
func designator(t cst.Token) string {
	switch t.Kind() {
	case token.StringLiteral:
		return strings.ToLower(t.Text())
	case token.CharacterLiteral, token.ExtendedIdentifier:
		return t.Text()
	}
	return strings.ToUpper(t.Text())
}

func upper(t cst.Token) string { return strings.ToUpper(t.Text()) }
