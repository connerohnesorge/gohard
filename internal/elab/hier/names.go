package hier

import (
	"strconv"
	"strings"

	"github.com/connerohnesorge/gohard/internal/eval"
	"github.com/connerohnesorge/gohard/internal/sema"
)

// names.go: the LRM 16.2.5 name strings, 'SIMPLE_NAME, 'PATH_NAME and
// 'INSTANCE_NAME. The forms follow NVC 1.23: lower case, ':' before
// every element. An instance is
//
//	top entity          :tb                 :tb(arch)
//	entity instance     :tb:dut             :tb(arch):dut@ent(arch)
//	block               :tb:b               :tb(arch):b
//	for generate        :tb:g(3)            :tb(arch):g(3)
//	if/case generate    :tb:g  (or :alt)    :tb(arch):g
//
// A for generate iteration shows the parameter's position (an
// integer's value, an enumeration literal's position), and a chosen
// alternative with an alternative label is named by that label
// instead of the generate's, as NVC does. An unbound component
// instance is its label. Inside an instance:
//
//   - a process is its label (empty when it has none: ":tb::v");
//   - a subprogram is its name and profile, "f[integer,bit return
//     string]", wherever it is called from;
//   - a library package is ":lib:pkg";
//   - loops, sequential blocks and other statements add nothing;
//   - a protected type (VHDL-2019) is the path of the object the
//     running method belongs to (Namer.Protected).
//
// A named entity that opens a region (an entity, architecture, block,
// generate, process, subprogram or package) ends with ':'.

// Names are the path and instance names of one thing.
type Names struct{ Path, Inst string }

// Add appends s to both names.
func (n Names) Add(s string) Names { return Names{n.Path + s, n.Inst + s} }

// Names returns the names of instance in, without the final ':'.
func (in *Instance) Names() Names {
	if in.parent == nil {
		p := ":" + Lower(string(in.Entity.Name))
		return Names{p, p + archSuffix(in)}
	}
	n := in.parent.Names().Add(":" + nameLabel(in))
	if in.Kind == KindEntity && in.Entity != nil {
		n.Inst += "@" + Lower(string(in.Entity.Name)) + archSuffix(in)
	}
	return n
}

// Parent returns the instance in is a child of (nil for a top).
func (in *Instance) Parent() *Instance { return in.parent }

// Declares reports whether in elaborates the declarations of design
// region r: its entity or architecture, block or generate body.
func (in *Instance) Declares(r *sema.Region) bool {
	if r == nil {
		return false
	}
	switch in.Kind {
	case KindEntity:
		return in.Entity != nil && r == in.Entity.Region ||
			in.Arch != nil && r == in.Arch.Region
	case KindBlock:
		return in.Stmt != nil && r == in.Stmt.Region
	case KindGenerate:
		if in.Alt != nil {
			return r == in.Alt.Region
		}
		return in.Stmt != nil && r == in.Stmt.Region
	}
	return false
}

// archSuffix is "(arch)" of an entity instance with an architecture.
func archSuffix(in *Instance) string {
	if in.Arch == nil {
		return ""
	}
	return "(" + Lower(string(in.Arch.Name)) + ")"
}

// nameLabel is the path element of a non-top instance.
func nameLabel(in *Instance) string {
	l := "_"
	if in.Stmt != nil {
		l = Lower(label(in.Stmt))
	}
	switch {
	case in.Alt != nil && in.Alt.Label != nil:
		return Lower(string(in.Alt.Label.Name))
	case in.Kind == KindGenerate && in.Alt == nil && in.Param.T != nil:
		return l + "(" + strconv.FormatInt(in.Param.I, 10) + ")"
	}
	return l
}

// SimpleName is E'SIMPLE_NAME: the designator in lower case, an
// operator symbol or character literal as written ("+", 'x').
func SimpleName(d *sema.Decl) string { return Lower(string(d.Name)) }

// Lower returns s in lower case (ASCII; NVC lowers extended
// identifiers too), the case of every name string.
func Lower(s string) string {
	if strings.IndexFunc(s, func(r rune) bool {
		return r >= 'A' && r <= 'Z'
	}) < 0 {
		return s
	}
	b := []byte(s)
	for i, c := range b {
		if c >= 'A' && c <= 'Z' {
			b[i] = c + 'a' - 'A'
		}
	}
	return string(b)
}

// NameAttr reports whether e is one of the name attributes.
func NameAttr(e *sema.Expr) bool {
	if e == nil || e.Kind != sema.ExprAttr {
		return false
	}
	switch e.Attr {
	case "simple_name", "path_name", "instance_name":
		return true
	}
	return false
}

// Namer computes the names of declarations in one context.
type Namer struct {
	// Find returns the instance that elaborates design region r, nil
	// if the context has none.
	Find func(r *sema.Region) *Instance
	// Protected returns the names of the protected object whose type
	// declares (or whose body is) r; nil means the context has no
	// protected objects.
	Protected func(r *sema.Region) (Names, error)
	// Instance returns the subprogram instance whose call runs the
	// body of the generic subprogram g (LRM 4.4: its declarations are
	// named with the instance's designator), nil when none does or
	// the context does not know; nil means none.
	Instance func(g *sema.Decl) *sema.Decl
	// pending is the instance whose generics hier is evaluating, not
	// yet among its parent's children.
	pending *Instance
}

// Within returns the Namer of the code of instance in: design regions
// are in or one of its parents; there are no protected objects.
func Within(in *Instance) Namer {
	return Namer{Find: func(r *sema.Region) *Instance {
		for c := in; c != nil; c = c.parent {
			if c.Declares(r) {
				return c
			}
		}
		return nil
	}}
}

// Value returns the string of name attribute e.
func (n Namer) Value(e *sema.Expr) (string, error) {
	if e.Prefix == nil || e.Prefix.Decl == nil {
		return "", sema.NewUnsupported("'"+strings.ToUpper(
			string(e.Attr))+" of "+e.Node.Text(), nil)
	}
	d := e.Prefix.Decl
	if e.Attr == "simple_name" {
		return SimpleName(d), nil
	}
	ns, err := n.Entity(d)
	if e.Attr == "instance_name" {
		return ns.Inst, err
	}
	return ns.Path, err
}

// Entity returns the names of the named entity d.
func (n Namer) Entity(d *sema.Decl) (Names, error) {
	switch d.Kind {
	case sema.DeclEntity, sema.DeclArchitecture:
		in, err := n.instance(d.Region)
		if err != nil {
			return Names{}, err
		}
		return in.Names().Add(":"), nil
	case sema.DeclFunction, sema.DeclProcedure:
		p, err := n.Region(d.Parent)
		if err != nil {
			return Names{}, err
		}
		return p.Add(":" + Profile(d) + ":"), nil
	case sema.DeclPackage, sema.DeclPackageBody, sema.DeclPackageInst:
		p, err := n.unit(d)
		if err != nil {
			return Names{}, err
		}
		return p.Add(":"), nil
	case sema.DeclLabel:
		return n.label(d)
	}
	p, err := n.Region(d.Parent)
	if err != nil {
		return Names{}, err
	}
	return p.Add(":" + SimpleName(d)), nil
}

// label returns the names of a statement label: a block or generate
// is its instance and ':', an instance statement its instance, a
// process (or other statement) its region's path, the label and ':'.
func (n Namer) label(d *sema.Decl) (Names, error) {
	if d.Parent != nil && n.Find != nil {
		for _, r := range d.Parent.Children {
			if r.Owner == d && (r.Kind == sema.RegionBlock ||
				r.Kind == sema.RegionGenerate) {
				if in := n.Find(r); in != nil {
					return in.Names().Add(":"), nil
				}
			}
		}
		if in := n.Find(d.Parent); in != nil {
			cs := in.Children
			if p := n.pending; p != nil && p.parent == in {
				cs = append([]*Instance{p}, cs...)
			}
			for _, c := range cs {
				if c.Stmt != nil && c.Stmt.Label == d &&
					c.Stmt.Kind == sema.StmtInstance {
					// An instance label is a leaf in NVC: ":tb:u".
					return c.Names(), nil
				}
			}
		}
	}
	p, err := n.Region(d.Parent)
	if err != nil {
		return Names{}, err
	}
	return p.Add(":" + SimpleName(d) + ":"), nil
}

// unit returns the names of a package (body or instance) d, without
// the final ':': ":lib:pkg" for a library unit, else its region's path
// and its name.
func (n Namer) unit(d *sema.Decl) (Names, error) {
	if d.Kind == sema.DeclPackageBody && d.Spec != nil {
		d = d.Spec
	}
	if d.Parent == nil || d.Parent.Kind == sema.RegionContext {
		lib := ""
		if d.Unit != nil {
			lib = string(d.Unit.Library)
		}
		s := ":" + Lower(lib) + ":" + SimpleName(d)
		return Names{s, s}, nil
	}
	p, err := n.Region(d.Parent)
	if err != nil {
		return Names{}, err
	}
	return p.Add(":" + SimpleName(d)), nil
}

// Region returns the names of the declarations of region r, without
// the final ':'.
func (n Namer) Region(r *sema.Region) (Names, error) {
	var segs []string // innermost first
	for ; r != nil; r = r.Parent {
		var base Names
		var err error
		switch r.Kind {
		case sema.RegionProcess:
			l := ""
			if r.Owner != nil {
				l = SimpleName(r.Owner)
			}
			segs = append(segs, ":"+l)
			continue
		case sema.RegionSubprogram:
			if d := r.Owner; d != nil {
				if n.Instance != nil {
					if x := n.Instance(d); x != nil {
						d = x
					}
				}
				segs = append(segs, ":"+Profile(d))
			}
			continue
		case sema.RegionLoop, sema.RegionSeqBlock, sema.RegionStatement:
			continue
		case sema.RegionEntity, sema.RegionArchitecture,
			sema.RegionBlock, sema.RegionGenerate:
			var in *Instance
			if in, err = n.instance(r); err == nil {
				base = in.Names()
			}
		case sema.RegionPackage, sema.RegionPackageBody:
			if genericPackage(r) {
				return Names{}, unnamed("a declaration of a generic " +
					"package")
			}
			if r.Owner == nil {
				return Names{}, notNamed(r)
			}
			base, err = n.unit(r.Owner)
		case sema.RegionProtected, sema.RegionProtectedBody:
			if n.Protected == nil {
				return Names{}, unnamed("a protected type member " +
					"outside a call of its object's methods")
			}
			base, err = n.Protected(r)
		default:
			return Names{}, notNamed(r)
		}
		if err != nil {
			return Names{}, err
		}
		for i := len(segs) - 1; i >= 0; i-- {
			base = base.Add(segs[i])
		}
		return base, nil
	}
	return Names{}, notNamed(nil)
}

func (n Namer) instance(r *sema.Region) (*Instance, error) {
	if n.Find != nil {
		if in := n.Find(r); in != nil {
			return in, nil
		}
	}
	return nil, notNamed(r)
}

// unnamed is the please-report error of the names of what.
func unnamed(what string) error {
	return sema.NewUnsupported("'PATH_NAME and 'INSTANCE_NAME of "+what,
		nil)
}

// notNamed is the error of a declaration in a region the names do not
// reach (a record, a component, a configuration, an instance the
// context does not run in).
func notNamed(r *sema.Region) error {
	what := "a declaration outside a design region"
	if r != nil && r.Owner != nil {
		what = "a declaration of " + r.Owner.Text + " here"
	}
	return unnamed(what)
}

// genericPackage reports whether r is the region of an uninstantiated
// package (or its body): its declarations exist per instance, which a
// running subprogram of the package does not know.
func genericPackage(r *sema.Region) bool {
	if r.Kind == sema.RegionPackageBody && r.Owner != nil &&
		r.Owner.Spec != nil {
		r = r.Owner.Spec.Region
	}
	if r == nil {
		return false
	}
	for _, d := range r.Decls {
		switch d.Kind {
		case sema.DeclGeneric, sema.DeclGenericType,
			sema.DeclGenericSubprogram, sema.DeclGenericPackage:
			return true
		}
	}
	return false
}

// Profile is a subprogram's name and parameter and result type marks,
// "f[integer,bit return string]" ("p[]" without parameters).
func Profile(d *sema.Decl) string {
	if d.Spec != nil && (d.Spec.Kind == sema.DeclFunction ||
		d.Spec.Kind == sema.DeclProcedure) {
		d = d.Spec
	}
	ps, rt := d, d // where the parameters and the result are
	if g := sema.InstanceOf(d); g != nil {
		// An instance has its own designator; its profile is that
		// of the subprogram it instantiates when it records none.
		if len(d.Params) == 0 {
			ps = g
		}
		if d.Type == nil {
			rt = g
		}
	}
	ts := make([]string, 0, len(ps.Params))
	for _, p := range ps.Params {
		ts = append(ts, Lower(p.Type.Name()))
	}
	s := strings.Join(ts, ",")
	if d.Kind == sema.DeclFunction {
		if s != "" {
			s += " "
		}
		s += "return " + Lower(rt.Type.Name())
	}
	return SimpleName(d) + "[" + s + "]"
}

// nameDyn evaluates the name attributes of the expressions hier
// evaluates in instance in (generic actuals): everything else is left
// to the static rules.
type nameDyn struct{ in, pending *Instance }

func (nameDyn) Ref(*sema.Decl) (eval.Ref, bool) { return nil, false }

func (nameDyn) Load(eval.Ref) (eval.Value, error) {
	return eval.Value{}, eval.NotStatic("a name attribute reference")
}

func (y nameDyn) Lower(_ *sema.Evaluator, e *sema.Expr, _ *eval.Budget,
) (eval.Expr, bool, error) {
	if !NameAttr(e) {
		return nil, false, nil
	}
	n := Within(y.in)
	n.pending = y.pending
	s, err := n.Value(e)
	if err != nil {
		return nil, true, err
	}
	return &eval.Const{V: eval.Str(s)}, true, nil
}

func (nameDyn) Call(eval.Func, []eval.Value, *eval.Budget) (eval.Value,
	bool, error,
) {
	return eval.Value{}, false, nil
}

// Named returns ev, evaluating the name attributes of expression x
// (if it has any) as code of instance in: the initial values of the
// objects elaboration evaluates outside a process.
func Named(ev *sema.Evaluator, in *Instance, x *sema.Expr,
) *sema.Evaluator {
	return named(ev, in, nil, x)
}

// named is Named while the generics of pending are evaluated.
func named(ev *sema.Evaluator, in, pending *Instance, x *sema.Expr,
) *sema.Evaluator {
	if ev == nil || in == nil || !hasNameAttr(x, 0) {
		return ev
	}
	return ev.WithDynamic(nameDyn{in, pending})
}

// hasNameAttr reports whether x has a name attribute.
func hasNameAttr(x *sema.Expr, depth int) bool {
	if x == nil || depth > 256 {
		return false
	}
	if NameAttr(x) {
		return true
	}
	if hasNameAttr(x.Prefix, depth+1) {
		return true
	}
	for _, a := range x.Args {
		if hasNameAttr(a, depth+1) {
			return true
		}
	}
	for _, as := range x.Assocs {
		if hasNameAttr(as.Value, depth+1) {
			return true
		}
	}
	return false
}
