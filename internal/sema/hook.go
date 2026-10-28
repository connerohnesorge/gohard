package sema

import (
	"iter"

	"github.com/connerohnesorge/gohard/internal/cst"
	"github.com/connerohnesorge/gohard/internal/token"
)

// StmtHook observes the statements a running design executes: the
// simulator's interpreter (internal/elab) reports the statements of
// processes, of concurrent statements and of the subprograms they
// call, and the evaluator reports those of the pure functions it
// interprets for them. It is the hook of code coverage (#62) and of
// the debugger (#68). A hook only observes: with or without one, a run
// gives the same values, trace and step counts.
type StmtHook interface {
	// Stmt is called when the statement s starts, once its step is
	// charged. For a concurrent statement (an assignment, assertion
	// or procedure call that runs as a process) s is the interpreter's
	// statement of its equivalent process, whose Node is the
	// concurrent statement's.
	Stmt(s *SeqStmt)
	// Branch is called when the decision s takes its alternative i:
	// for an if statement the branch whose condition held, or
	// len(s.Branches) for the else part (written or not); for a case
	// statement or a selected assignment the alternative chosen; for
	// a conditional assignment the branch whose condition held, or the
	// number of branches when none held. Selected and conditional
	// signal assignments count their alternatives in the syntax tree
	// (Alternatives).
	Branch(s *SeqStmt, i int)
}

// SetStmtHook makes ev, and every evaluator sharing its design state
// (those derived from it), report the statements of the functions they
// interpret to h (nil: none). Pure calls are then never answered from
// the memo (WithMemo), so h sees the statements of every call; the
// memo charges the steps of the call it replaces, so step counts do
// not change.
func (ev *Evaluator) SetStmtHook(h StmtHook) { ev.sh.hook = h }

// StmtHook returns the hook SetStmtHook installed (nil: none).
func (ev *Evaluator) StmtHook() StmtHook { return ev.sh.hook }

// branch reports that the decision s took its alternative i.
func (ev *Evaluator) branch(s *SeqStmt, i int) {
	if h := ev.sh.hook; h != nil {
		h.Branch(s, i)
	}
}

// Alternatives returns the number of alternatives of the decision s,
// the values Branch reports for it run from 0 to the result minus
// one: an if statement has one per branch plus its else part (written
// or not); a case statement and a selected assignment one per
// alternative; a conditional assignment one per branch, plus one for
// no assignment when its last branch has a condition. Any other
// statement has none (0). A signal assignment's alternatives are
// counted in its syntax tree, so s may be the statement of a
// concurrent assignment's equivalent process.
func Alternatives(s *SeqStmt) int {
	switch s.Kind {
	case SeqIf:
		return len(s.Branches) + 1
	case SeqCase, SeqSelVarAssign:
		return len(s.Alts)
	case SeqCondVarAssign:
		n := len(s.Branches)
		if n == 0 || s.Branches[n-1].Cond != nil {
			n++
		}
		return n
	case SeqSignal:
		return signalAlternatives(s.Node)
	}
	return 0
}

// signalAlternatives counts the alternatives of a conditional or
// selected signal assignment (0 for another statement).
func signalAlternatives(n cst.Node) int {
	var whens iter.Seq[cst.WhenBranch]
	var sels iter.Seq[cst.SelectBranch]
	switch n.Kind() {
	case cst.KindConditionalSignalAssignStmt:
		whens = cst.ConditionalSignalAssignStmt{Node: n}.Waveforms().
			Branches()
	case cst.KindConcurrentConditionalSignalAssign:
		whens = cst.ConcurrentConditionalSignalAssign{Node: n}.
			Waveforms().Branches()
	case cst.KindSelectedSignalAssignStmt:
		sels = cst.SelectedSignalAssignStmt{Node: n}.Waveforms().
			Branches()
	case cst.KindConcurrentSelectedSignalAssign:
		sels = cst.ConcurrentSelectedSignalAssign{Node: n}.Waveforms().
			Branches()
	default:
		return 0
	}
	k := 0
	if sels != nil {
		for range sels {
			k++
		}
		return k
	}
	cond := true
	for br := range whens {
		k++
		cond = !br.Node.Token(token.KwWhen, 0).IsZero()
	}
	if cond {
		k++ // the last branch has a condition: none may hold
	}
	return k
}
