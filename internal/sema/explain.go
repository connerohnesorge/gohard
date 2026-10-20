package sema

import "github.com/connerohnesorge/gohard/internal/diag"

// Explain returns the diagnostic an undecided overload result reports
// (#34): sema/no-overload with every candidate and why it does not fit
// when none fits, sema/ambiguous-overload with the fitting candidates
// when more than one does. Hover shows this same result, so the editor
// and `gohard check` name the same candidates and reasons. ok is false
// for a decided result or one without candidates.
func (o *Overload) Explain() (d diag.Diagnostic, ok bool) {
	if o == nil || o.Chosen != nil || len(o.Candidates) == 0 {
		return diag.Diagnostic{}, false
	}
	if o.Ambiguous() {
		return diag.New(diag.SemaAmbiguousOverload, o.Name,
			fitting(o)), true
	}
	for _, c := range o.Candidates {
		if c.Fits {
			return diag.Diagnostic{}, false
		}
	}
	return diag.New(diag.SemaNoOverload, o.Name, reasons(o)), true
}

// Sorted returns the candidates in the order the diagnostics list
// them: the fitting ones, then the nearest misses.
func (o *Overload) Sorted() []Candidate {
	if o == nil {
		return nil
	}
	return sortedCandidates(o)
}

// Describe is how diagnostics name an overload: its designator,
// signature and origin ("inc [INTEGER return INTEGER] from pkg").
func Describe(d *Decl) string { return describe(d) }
