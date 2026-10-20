package parser

// Source returns the bytes r was parsed from. They are shared: callers
// must not modify them.
func (r *Result) Source() []byte { return r.src }

// Restore returns a copy of r whose parsed bytes are src: the codec of
// the prebuilt std/ieee bundle (#33) restores stored parse results this
// way. Nothing else needs it; results otherwise come from Parse.
func Restore(r Result, src []byte) *Result {
	r.src = src
	return &r
}
