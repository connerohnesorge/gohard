package libs

import "strings"

// Edit replaces the bytes [Start, End) of a text with NewText.
type Edit struct {
	Start, End int
	NewText    string
}

// Apply returns text with e applied.
func (e Edit) Apply(text []byte) []byte {
	out := append([]byte(nil), text[:e.Start]...)
	out = append(out, e.NewText...)
	return append(out, text[e.End:]...)
}

// LibrariesEdit returns the edit of the gohard.yaml text that adds
// lines (each "lib: [folder]" or a comment) under the top-level
// libraries key: new lines right after a block-style key (indented
// like its first entry), `{}` replaced by a block, or a new libraries
// block at the end when the key is missing. ok is false for a
// non-empty flow mapping, which is not rewritten. `gohard libs detect
// --write` and the language server's quick fix share it (#1, #34).
func LibrariesEdit(text []byte, lines ...string) (Edit, bool) {
	ls := strings.SplitAfter(string(text), "\n")
	off := 0
	for i, l := range ls {
		val, ok := topValue(l, "libraries")
		if !ok {
			off += len(l)
			continue
		}
		body := strings.TrimRight(l, "\r\n")
		indent := "  "
		for _, n := range ls[i+1:] {
			t := strings.TrimLeft(n, " \t")
			if strings.TrimSpace(t) == "" || strings.HasPrefix(t, "#") {
				continue
			}
			if w := len(n) - len(t); w > 0 {
				indent = n[:w]
			}
			break
		}
		block := indent + strings.Join(lines, "\n"+indent) + "\n"
		switch val {
		case "":
			at := off + len(l)
			if len(body) == len(l) { // the last line, no newline
				return Edit{at, at, "\n" + block}, true
			}
			return Edit{at, at, block}, true
		case "{}":
			colon := off + strings.Index(l, ":")
			return Edit{colon + 1, off + len(body), "\n  " +
				strings.Join(lines, "\n  ")}, true
		}
		return Edit{}, false
	}
	return Edit{len(text), len(text), newline(text) + "libraries:\n  " +
		strings.Join(lines, "\n  ") + "\n"}, true
}

// OpaqueEdit returns the edit that adds units to the top-level
// allow_opaque list: into a one-line flow list, after the last item
// of a block list, or as a new key at the end. ok is false for other
// shapes.
func OpaqueEdit(text []byte, units []string) (Edit, bool) {
	ls := strings.SplitAfter(string(text), "\n")
	off := 0
	for i, l := range ls {
		val, ok := topValue(l, "allow_opaque")
		if !ok {
			off += len(l)
			continue
		}
		body := strings.TrimRight(l, "\r\n")
		switch {
		case val == "[]":
			at := off + strings.LastIndex(body, "[") + 1
			return Edit{at, at, strings.Join(units, ", ")}, true
		case strings.HasPrefix(val, "[") && strings.HasSuffix(val, "]"):
			at := off + strings.LastIndex(body, "]")
			return Edit{at, at, ", " + strings.Join(units, ", ")}, true
		case val != "":
			return Edit{}, false
		}
		at, indent := off+len(l), "  - "
		if len(body) == len(l) {
			return Edit{at, at, "\n" + indent + strings.Join(units,
				"\n"+indent) + "\n"}, true
		}
		for _, n := range ls[i+1:] {
			t := strings.TrimLeft(n, " \t")
			if strings.TrimSpace(t) == "" || strings.HasPrefix(t, "#") {
				at += len(n)
				continue
			}
			if !strings.HasPrefix(t, "- ") {
				break
			}
			indent = n[:len(n)-len(t)] + "- "
			at += len(n)
		}
		block := indent + strings.Join(units, "\n"+indent) + "\n"
		if at > 0 && text[at-1] != '\n' {
			block = "\n" + block
		}
		return Edit{at, at, block}, true
	}
	return Edit{len(text), len(text), newline(text) + "allow_opaque: [" +
		strings.Join(units, ", ") + "]\n"}, true
}

// topValue reports whether line sets the top-level key and returns
// its value without a trailing comment.
func topValue(line, key string) (string, bool) {
	body := strings.TrimRight(line, "\r\n")
	rest, ok := strings.CutPrefix(body, key)
	if !ok {
		return "", false
	}
	rest = strings.TrimLeft(rest, " \t")
	if !strings.HasPrefix(rest, ":") {
		return "", false
	}
	v := rest[1:]
	if i := strings.Index(v, "#"); i >= 0 {
		v = v[:i]
	}
	return strings.TrimSpace(v), true
}

func newline(text []byte) string {
	if len(text) > 0 && text[len(text)-1] != '\n' {
		return "\n"
	}
	return ""
}
