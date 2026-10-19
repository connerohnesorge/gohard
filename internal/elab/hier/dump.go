package hier

import (
	"strings"

	"github.com/connerohnesorge/gohard/internal/sema"
)

// Dump returns the hierarchy in its stable text form, the form the
// elaboration oracle compares (#8) and the corpus goldens record: one
// instance per line in preorder, indented two spaces per level,
//
//	<path> <kind>[ <lib>.<entity>(<arch>)][ config <lib>.<cfg>]
//	    [ <generic>=<value>...]
//
// (one line), where kind is entity, block, for-generate, if-generate,
// case-generate or unbound; generics are the constant generics in
// declaration order, lower case, with their 'IMAGE (strings and
// vectors quoted) or ? when not known; generic types show the type
// mark. An unbound component instance shows the component. The text
// depends only on the design (P1).
func (r *Result) Dump() string {
	var sb strings.Builder
	var walk func(in *Instance, depth int)
	walk = func(in *Instance, depth int) {
		sb.WriteString(strings.Repeat("  ", depth))
		sb.WriteString(in.Path)
		sb.WriteString(" " + in.kindWord())
		switch in.Kind {
		case KindEntity:
			sb.WriteString(" " + string(in.Entity.Unit.Library) + "." +
				string(in.Entity.Name))
			if in.Arch != nil {
				sb.WriteString("(" + string(in.Arch.Name) + ")")
			}
			if in.Configuration != nil {
				sb.WriteString(" config " +
					string(in.Configuration.Library) + "." +
					string(in.Configuration.Name))
			}
		case KindUnbound:
			if in.Component != nil {
				sb.WriteString(" component " + string(in.Component.Name))
			}
		}
		for _, g := range in.Generics {
			switch g.Decl.Kind {
			case sema.DeclGeneric:
				sb.WriteString(" " + string(g.Decl.Name) + "=")
				if g.Known {
					sb.WriteString(g.Value.Image())
				} else {
					sb.WriteString("?")
				}
			case sema.DeclGenericType:
				sb.WriteString(" " + string(g.Decl.Name) + "=")
				if g.Type != nil {
					sb.WriteString(g.Type.Name())
				} else {
					sb.WriteString("?")
				}
			}
		}
		sb.WriteByte('\n')
		for _, c := range in.Children {
			walk(c, depth+1)
		}
	}
	for _, t := range r.Tops {
		walk(t, 0)
	}
	return sb.String()
}

// Paths returns the instance paths in preorder: what the elaboration
// oracle compares with GHDL's --disp-tree=inst instances.
func (r *Result) Paths() []string {
	var out []string
	r.Walk(func(in *Instance) { out = append(out, in.Path) })
	return out
}

func (in *Instance) kindWord() string {
	switch in.Kind {
	case KindEntity:
		return "entity"
	case KindBlock:
		return "block"
	case KindUnbound:
		return "unbound"
	case KindGenerate:
		if in.Stmt != nil {
			switch in.Stmt.Kind {
			case sema.StmtForGenerate:
				return "for-generate"
			case sema.StmtIfGenerate:
				return "if-generate"
			case sema.StmtCaseGenerate:
				return "case-generate"
			}
		}
		return "generate"
	}
	return "?"
}
