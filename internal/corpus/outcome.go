package corpus

import (
	"fmt"
	"slices"
	"strings"

	"github.com/connerohnesorge/gohard/v2/internal/config"
	"github.com/connerohnesorge/gohard/v2/internal/trace"
)

// Match says how strictly produced messages must equal the expected
// list.
type Match struct {
	// Subsequence lets other messages appear between and after the
	// listed ones (variants: parser recovery may add errors, #8).
	Subsequence bool
	// IgnoreLines drops line comparison and output order (a variant
	// that moves statements, whose lines change but not the outcome).
	IgnoreLines bool
}

// MatchFor is the match mode of a run: variants list at least the
// first error, the main run lists every message.
func MatchFor(r Run) Match { return Match{Subsequence: r.Variant != ""} }

// Check compares a result with an expected outcome and returns one
// line per difference.
func Check(exp Expected, res *Result, m Match) []string {
	var probs []string
	if res.Exit != exp.Exit {
		probs = append(probs, fmt.Sprintf("exit %d, want %d", res.Exit,
			exp.Exit))
	}
	if string(res.Status) != exp.Status {
		probs = append(probs, fmt.Sprintf("status %s, want %s",
			res.Status, exp.Status))
	}
	probs = append(probs, checkStop(exp.StopTime, res)...)
	return append(probs, checkMessages(exp.Messages, res.Messages, m)...)
}

func checkStop(want string, res *Result) []string {
	if want == "none" || want == "" {
		if res.Simulated {
			return []string{fmt.Sprintf("simulated to %s, want no "+
				"simulation", timeText(res.StopTime))}
		}
		return nil
	}
	t, ok := config.ParseTime(want)
	if !ok {
		return []string{fmt.Sprintf("corpus.yaml stop_time %q is not "+
			"a time", want)}
	}
	if !res.Simulated {
		return []string{fmt.Sprintf("no simulation, want stop at %s",
			want)}
	}
	if trace.Time(t) != res.StopTime {
		return []string{fmt.Sprintf("stopped at %s, want %s",
			timeText(res.StopTime), want)}
	}
	return nil
}

func timeText(t trace.Time) string {
	if t == 0 {
		return "0 fs"
	}
	return config.Time(t).String()
}

// matches reports whether produced message p is expected message e.
func matches(e Message, p Produced, m Match) bool {
	sev := e.Severity
	if sev == "note" {
		sev = "hint" // VHDL note reports are hints (#20)
	}
	if p.Severity.String() != sev ||
		!strings.Contains(p.Message, e.Text) {
		return false
	}
	if e.Code != "" && string(p.Code) != e.Code {
		return false
	}
	if e.Time != "" {
		t, ok := config.ParseTime(e.Time)
		if !ok || !p.Timed || trace.Time(t) != p.Time {
			return false
		}
	}
	if e.Path != "" && p.Path != e.Path {
		return false
	}
	return m.IgnoreLines || e.Line == 0 || p.Line == e.Line
}

func show(p Produced) string {
	s := fmt.Sprintf("%s %s %q", p.Severity, p.Code, p.Message)
	if p.Path != "" {
		s += fmt.Sprintf(" at %s:%d", p.Path, p.Line)
	}
	if p.Timed {
		s += " time " + timeText(p.Time)
	}
	return s
}

func showExp(e Message) string {
	s := fmt.Sprintf("%s %q", e.Severity, e.Text)
	if e.Code != "" {
		s += " " + e.Code
	}
	if e.Path != "" {
		s += fmt.Sprintf(" at %s:%d", e.Path, e.Line)
	}
	if e.Time != "" {
		s += " time " + e.Time
	}
	return s
}

func checkMessages(exp []Message, got []Produced, m Match) []string {
	var probs []string
	if m.IgnoreLines {
		// Any order: every expected message once, nothing else
		// unless Subsequence.
		left := slices.Clone(got)
		for _, e := range exp {
			i := slices.IndexFunc(left, func(p Produced) bool {
				return matches(e, p, m)
			})
			if i < 0 {
				probs = append(probs, "missing message "+showExp(e))
				continue
			}
			left = slices.Delete(left, i, i+1)
		}
		if !m.Subsequence {
			for _, p := range left {
				probs = append(probs, "unexpected message "+show(p))
			}
		}
		return probs
	}
	j := 0
	for _, p := range got {
		if j < len(exp) && matches(exp[j], p, m) {
			j++
			continue
		}
		if !m.Subsequence {
			probs = append(probs, "unexpected message "+show(p))
		}
	}
	for _, e := range exp[j:] {
		probs = append(probs, "missing message "+showExp(e))
	}
	return probs
}
