package sema

// stmtrules.go holds statement rules of #34 that depend on where the
// statement is: the processes of an entity statement part are passive
// (LRM 3.2, 11.3), and a guarded assignment needs a visible GUARD
// signal (LRM 11.6).

import (
	"github.com/connerohnesorge/gohard/internal/cst"
	"github.com/connerohnesorge/gohard/internal/token"
)

// passiveProcess reports the signal assignments of a process in an
// entity statement part, which must be passive (LRM 3.2, 11.3). parent
// is the region the process is declared in.
func (a *analyzer) passiveProcess(parent *Region, seq []*SeqStmt) {
	if a.checksOff() || parent == nil || parent.Kind != RegionEntity {
		return
	}
	walkSeq(seq, func(q *SeqStmt) {
		if q.Kind == SeqSignal {
			a.semaError(q.Node.Span(), "a process in an entity "+
				"statement part is passive and cannot assign a signal")
		}
	}, func(*Expr) {})
}

// guardVisible reports a guarded concurrent signal assignment st with
// no signal GUARD visible (LRM 11.6).
func (a *analyzer) guardVisible(st cst.Node) {
	if a.checksOff() || st.Token(token.KwGuarded, 0).IsZero() {
		return
	}
	for _, d := range a.lookup("guard").Decls {
		if objectClass(d.Denotes()) == "signal" {
			return
		}
	}
	a.semaError(st.Token(token.KwGuarded, 0).Span(), "a guarded "+
		"assignment needs a visible signal GUARD")
}
