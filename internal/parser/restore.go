package parser

// Source returns the bytes r was parsed from. They are shared: callers
// must not modify them.
func (r *Result) Source() []byte { return r.src }

// Restore sets the parsed bytes of a decoded r to src and returns r:
// the codec of the prebuilt std/ieee bundle (#33) restores stored parse
// results this way. Nothing else needs it; results otherwise come from
// Parse.
func Restore(r *Result, src []byte) *Result {
	r.src = src
	return r
}
