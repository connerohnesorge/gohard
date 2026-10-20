package sim

import "github.com/connerohnesorge/gohard/internal/eval"

// isLeaf reports whether v is a scalar subelement (not an array or a
// record).
func isLeaf(v eval.Value) bool {
	if v.T == nil {
		return len(v.E) == 0
	}
	k := v.T.Kind
	return k != eval.KindArray && k != eval.KindRecord
}

// flatten appends the scalar subelements of v in order (row-major
// arrays, record fields in declaration order).
func flatten(v eval.Value, out []eval.Value) []eval.Value {
	if isLeaf(v) {
		return append(out, v)
	}
	for _, e := range v.E {
		out = flatten(e, out)
	}
	return out
}

// leaves counts the scalar subelements of v.
func leaves(v eval.Value) int {
	if isLeaf(v) {
		return 1
	}
	if v.T != nil && v.T.Kind == eval.KindArray {
		// Every element of an array has the element subtype, so the
		// same number of leaves.
		if len(v.E) == 0 {
			return 0
		}
		return len(v.E) * leaves(v.E[0])
	}
	n := 0
	for _, e := range v.E {
		n += leaves(e)
	}
	return n
}

// rebuild returns tmpl with its leaves replaced by flat, in order, and
// the number of leaves used.
func rebuild(tmpl eval.Value, flat []eval.Value) (eval.Value, int) {
	if isLeaf(tmpl) {
		v := flat[0]
		if tmpl.T != nil {
			v.T = tmpl.T
		}
		return v, 1
	}
	out := tmpl
	out.E = make([]eval.Value, len(tmpl.E))
	n := 0
	for i, e := range tmpl.E {
		var used int
		out.E[i], used = rebuild(e, flat[n:])
		n += used
	}
	return out, n
}

// leafAt returns the template leaf i of tmpl (its subtype gives the
// scalar type of the element).
func leafAt(tmpl eval.Value, i int) (eval.Value, bool) {
	if isLeaf(tmpl) {
		return tmpl, i == 0
	}
	if tmpl.T != nil && tmpl.T.Kind == eval.KindArray && len(tmpl.E) > 0 {
		n := leaves(tmpl.E[0]) // the same for every element
		if i < 0 || n == 0 || i/n >= len(tmpl.E) {
			return eval.Value{}, false
		}
		return leafAt(tmpl.E[i/n], i%n)
	}
	for _, e := range tmpl.E {
		n := leaves(e)
		if i < n {
			return leafAt(e, i)
		}
		i -= n
	}
	return eval.Value{}, false
}
