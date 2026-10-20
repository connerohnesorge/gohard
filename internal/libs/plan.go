package libs

import (
	"errors"
	"slices"
	"strings"
)

// Plan is what a detection proposes for gohard.yaml once versions are
// picked: the libraries lines, the encrypted units for allow_opaque,
// and what is left out and why.
type Plan struct {
	// Groups are the chosen installs with their lines, in Result
	// order.
	Groups []Group
	// Ambiguous are the products with several versions and no
	// --version pick: none of their lines is proposed.
	Ambiguous [][]Install
	// Opaque are the encrypted units of the chosen libraries, sorted.
	Opaque []string
}

// Group is one chosen install.
type Group struct {
	Install Install
	// Entries are its mappable libraries not mapped by an earlier
	// group.
	Entries []Library
	// Left are its other libraries, each with the reason it gets no
	// line.
	Left []Left
}

// Left is a library without a config line.
type Left struct {
	Library Library
	Why     string
}

// Choose picks one version per product (#1). versions are --version
// values: "VER" picks VER of every product that has it, "PRODUCT=VER"
// of that product only. A product with one install needs no pick. It
// is an error when a value matches no install.
func (r Result) Choose(versions []string) (Plan, error) {
	type pick struct{ product, version string }
	var picks []pick
	used := make([]bool, len(versions))
	for _, v := range versions {
		p, ver, ok := strings.Cut(v, "=")
		if !ok {
			p, ver = "", v
		}
		if ver == "" || ok && !slices.Contains(Products(), p) {
			return Plan{}, errors.New("--version " + v +
				": want VERSION or PRODUCT=VERSION with PRODUCT one " +
				"of " + strings.Join(Products(), ", "))
		}
		picks = append(picks, pick{p, ver})
	}
	var plan Plan
	byName := map[string]string{} // library -> folder already mapped
	for _, group := range r.products() {
		chosen := group
		if len(group) > 1 && group[0].Product != "ghdl" {
			chosen = nil
			for _, in := range group {
				for i, pk := range picks {
					if (pk.product == "" || pk.product == in.Product) &&
						pk.version == in.Version {
						used[i] = true
						if !slices.ContainsFunc(chosen, func(c Install,
						) bool {
							return c.Dir == in.Dir
						}) {
							chosen = append(chosen, in)
						}
					}
				}
			}
			if len(chosen) != 1 {
				plan.Ambiguous = append(plan.Ambiguous, group)
				continue
			}
		} else {
			for _, in := range group {
				for i, pk := range picks {
					if (pk.product == "" ||
						pk.product == in.Product) &&
						pk.version == in.Version {
						used[i] = true
					}
				}
			}
		}
		for _, in := range chosen {
			plan.Groups = append(plan.Groups, plan.group(in, byName))
		}
	}
	for i, u := range used {
		if !u {
			return Plan{}, errors.New("--version " + versions[i] +
				" matches no detected install")
		}
	}
	slices.Sort(plan.Opaque)
	plan.Opaque = slices.Compact(plan.Opaque)
	return plan, nil
}

func (plan *Plan) group(in Install, byName map[string]string) Group {
	g := Group{Install: in}
	for _, l := range in.Libraries {
		key := strings.ToLower(l.Name)
		switch {
		case l.Stub:
			g.Left = append(g.Left, Left{l, "a GoHard CI stub " +
				"(testdata/stubs), never a substitute for the " +
				"vendor files"})
		case l.Unreadable != "":
			g.Left = append(g.Left, Left{l, l.Unreadable})
		case byName[key] == l.Folder:
			continue // the same folder through another index
		case byName[key] != "":
			g.Left = append(g.Left, Left{l, "already mapped to " +
				byName[key] + " above"})
		default:
			byName[key] = l.Folder
			g.Entries = append(g.Entries, l)
			plan.Opaque = append(plan.Opaque, l.Opaque...)
		}
	}
	return g
}

// products splits the installs into one group per product, in order.
func (r Result) products() [][]Install {
	var out [][]Install
	for _, in := range r.Installs {
		if n := len(out); n > 0 && out[n-1][0].Product == in.Product {
			out[n-1] = append(out[n-1], in)
			continue
		}
		out = append(out, []Install{in})
	}
	return out
}

// Label names an install in comments and code actions.
func (in Install) Label() string {
	s := in.Name
	if in.Version != "" {
		s += " " + in.Version
	}
	return s + " at " + in.Dir
}

// Entry is the libraries: line of l: `name: ['folder']`, the folder
// single-quoted so Windows drive letters, backslashes and spaces stay
// literal YAML text.
func (l Library) Entry() string {
	return l.Name + ": [" + Quote(l.Folder) + "]"
}

// Quote writes s as a YAML single-quoted scalar.
func Quote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}

// Text renders the plan as gohard.yaml lines with comments: the
// output of `gohard libs detect` (#1).
func (plan Plan) Text() string {
	var live bool
	for _, g := range plan.Groups {
		live = live || len(g.Entries) > 0
	}
	var b strings.Builder
	ind := ""
	if live {
		b.WriteString("libraries:\n")
		ind = "  "
	}
	if len(plan.Groups) == 0 && len(plan.Ambiguous) == 0 {
		b.WriteString("# no vendor libraries found\n")
	}
	for _, g := range plan.Groups {
		b.WriteString(ind + "# " + g.Install.Label() + " (found via " +
			g.Install.Via + ")\n")
		for _, l := range g.Entries {
			b.WriteString(ind + l.Entry() + "\n")
		}
		for _, l := range g.Left {
			b.WriteString(ind + "# not mapped: " + l.Library.Name +
				" at " + l.Library.Folder + " is " + l.Why + "\n")
		}
	}
	for _, group := range plan.Ambiguous {
		var vs []string
		for _, in := range group {
			vs = append(vs, in.Version)
		}
		b.WriteString(ind + "# " + group[0].Name + ": " +
			"several versions found (" + strings.Join(vs, ", ") +
			"); pick one with --version " + group[0].Product +
			"=VERSION\n")
		for _, in := range group {
			b.WriteString(ind + "#   " + in.Label() + " (found via " +
				in.Via + ")\n")
			for _, l := range in.Libraries {
				if l.Mappable() {
					b.WriteString(ind + "#     " + l.Entry() + "\n")
				}
			}
		}
	}
	if len(plan.Opaque) > 0 {
		b.WriteString("allow_opaque: [" + strings.Join(plan.Opaque,
			", ") + "]\n")
	}
	return b.String()
}
