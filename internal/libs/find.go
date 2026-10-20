package libs

import "strings"

// Candidate is one install's folder for a library.
type Candidate struct {
	Install Install
	Library Library
}

// Find returns every mappable folder of library name (compared
// case-insensitively) that Detect finds on h, in Result order and once
// per folder: the search behind the language server's "library not
// found" quick fix (#1, #34). Stubs and unreadable libraries are never
// candidates.
func Find(h Host, root, name string) []Candidate {
	var out []Candidate
	seen := map[string]bool{}
	for _, in := range Detect(h, root).Installs {
		for _, l := range in.Libraries {
			if !strings.EqualFold(l.Name, name) || !l.Mappable() ||
				seen[l.Folder] {
				continue
			}
			seen[l.Folder] = true
			out = append(out, Candidate{Install: in, Library: l})
		}
	}
	return out
}
