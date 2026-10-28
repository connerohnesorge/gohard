package stdlib

import (
	"slices"
	"strconv"
	"strings"
	"sync"

	"github.com/connerohnesorge/gohard/v2/internal/token"
)

// tagsFile maps every bundled declaration to the first standard that
// has it (#33). Each line is "<standard>\t<source>\t<key>" (Decl.Key),
// in Decls order; '#' lines are comments. The source column says where
// the tag comes from (README.md, "First-standard tags").
const tagsFile = "firststd.tsv"

// Tag is one line of firststd.tsv.
type Tag struct {
	First  token.Standard
	Source string
	Key    string
}

// Tags returns the lines of firststd.tsv in file order. Malformed lines
// are skipped; TestTags requires there are none.
func Tags() []Tag {
	raw, err := sources.ReadFile(tagsFile)
	if err != nil {
		return nil
	}
	tags, _ := parseTags(string(raw))
	return tags
}

// parseTags parses firststd.tsv and returns the one-based numbers of
// malformed lines.
func parseTags(s string) ([]Tag, []int) {
	var (
		out []Tag
		bad []int
	)
	n := 0
	for line := range strings.Lines(s) {
		n++
		line = strings.TrimRight(line, "\r\n")
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		f := strings.SplitN(line, "\t", 3)
		if len(f) != 3 || f[1] == "" || f[2] == "" {
			bad = append(bad, n)
			continue
		}
		std, err := strconv.Atoi(f[0])
		if err != nil || !slices.Contains(token.Standards,
			token.Standard(std)) {
			bad = append(bad, n)
			continue
		}
		out = append(out, Tag{First: token.Standard(std), Source: f[1],
			Key: f[2]})
	}
	return out, bad
}

var (
	declsOnce sync.Once
	allDecls  []Decl
)

// Decls returns every bundled declaration in analysis order (Files
// order, then source order), each with its first-standard tag from
// firststd.tsv. A declaration without a tag line gets 2019, the
// newest, so a missing line can only hide a name, never show one too
// early; TestTags requires a line for every declaration. The result is
// computed once and shared: callers must not modify it.
func Decls() []Decl {
	declsOnce.Do(func() {
		first := map[string]token.Standard{}
		for _, t := range Tags() {
			first[t.Key] = t.First
		}
		allDecls = untagged()
		for i := range allDecls {
			std, ok := first[allDecls[i].Key()]
			if !ok {
				std = token.Std2019
			}
			allDecls[i].First = std
		}
	})
	return allDecls
}

// untagged returns every bundled declaration with First unset.
func untagged() []Decl {
	var out []Decl
	for _, f := range manifest {
		if f.Body {
			continue
		}
		src, _ := Source(f.Path)
		out = append(out, declsOf(f.Library, f.Path, src,
			token.Std2019)...)
	}
	return out
}

// View is the bundled library as seen under one standard (#33): every
// declaration whose first standard is newer than the view's is hidden.
// A view hides names, not text: every view analyzes the same 2019
// source, so a 1993 design sees numeric_std's UNSIGNED but not
// UNRESOLVED_UNSIGNED, the 2008 type it is declared from.
type View struct{ std token.Standard }

// ViewOf returns the view of standard std.
func ViewOf(std token.Standard) View { return View{std: std} }

// Standard returns the view's standard.
func (v View) Standard() token.Standard { return v.std }

// Visible reports whether d is visible in v.
func (v View) Visible(d Decl) bool { return d.First <= v.std }

// Decls returns the declarations visible in v, in Decls order.
func (v View) Decls() []Decl {
	var out []Decl
	for _, d := range Decls() {
		if v.Visible(d) {
			out = append(out, d)
		}
	}
	return out
}

// HasUnit reports whether the package lib.unit (lower-case) exists in
// v, so `library ieee; use ieee.fixed_pkg.all;` fails under 1993.
func (v View) HasUnit(lib, unit string) bool {
	for _, d := range Decls() {
		if d.Kind == "package" && d.Library == lib && d.Unit == unit {
			return v.Visible(d)
		}
	}
	return false
}
