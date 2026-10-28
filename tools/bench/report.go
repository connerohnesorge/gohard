package main

import (
	"fmt"
	"math"
	"path"
	"strings"
)

func col(ss []Sample, f func(Sample) float64) []float64 {
	out := make([]float64, len(ss))
	for i, s := range ss {
		out[i] = f(s)
	}
	return out
}

// ofKind returns the samples of edits of one kind.
func ofKind(ss []Sample, kind string) []Sample {
	var out []Sample
	for _, s := range ss {
		if s.Kind == kind {
			out = append(out, s)
		}
	}
	return out
}

func p95(xs []float64) float64 { return Percentile(xs, 95) }
func p50(xs []float64) float64 { return Percentile(xs, 50) }

func fmtMS(x float64) string {
	if math.IsNaN(x) {
		return "n/a"
	}
	if x >= 1000 {
		return fmt.Sprintf("%.2f s", x/1000)
	}
	return fmt.Sprintf("%.1f ms", x)
}

// tokensFile names the document of the semantic tokens gate (#55).
var tokensFile = func() string {
	p, _ := projectByName("large")
	return path.Base(p.TokensFile)
}()

// hintsFile names the document of the inlay hints gate (#56).
var hintsFile = func() string {
	p, _ := projectByName("large")
	return path.Base(p.Target)
}()

// Gates returns the #25 latency gates, the #55 semantic tokens gate
// and the #56 inlay hints gate measured on the large project result r;
// sim, when its testbench ran, adds first simulation progress on a
// testbench that simulates.
func Gates(r result, sim *result) []Gate {
	g := func(name string, limit float64, xs []float64) Gate {
		return Gate{Name: name, LimitMS: limit, P95MS: p95(xs),
			Measured: len(xs) > 0}
	}
	gs := []Gate{
		g("Hover, warm p95 < 100 ms", 100, r.Warm.Hover),
		g("Definition, warm p95 < 100 ms", 100, r.Warm.Definition),
		g("Completion, warm p95 < 100 ms", 100, r.Warm.Completion),
		g("Semantic tokens, full file "+tokensFile+
			", warm p95 < 100 ms", 100, r.Warm.Tokens),
		g("Inlay hints, full file "+hintsFile+", every kind, warm "+
			"p95 < 100 ms", 100, r.Warm.Hints),
		g("Diagnostics after save, warm p95 < 2 s", 2000, r.Warm.Save),
		g("First diagnostics from cold start, p95 < 2 s", 2000, r.First),
		g("First simulation progress (first simulated-time report "+
			"or the run's answer), p95 < 500 ms", 500, r.Sim.SimKernel),
	}
	if sim != nil && len(sim.Sim.SimKernel) > 0 {
		gs = append(gs, g("First simulation progress on corpus "+
			sim.Project+" (first simulated-time report), p95 < 500 ms",
			500, sim.Sim.SimKernel))
	}
	return gs
}

// report renders the #19 Markdown report.
func report(header string, rs []result) string {
	var b strings.Builder
	b.WriteString("## #19 latency benchmark\n\n" + header + "\n")
	b.WriteString("\"Ready\" is the time from the didChange to the " +
		"moment the check's diagnostics are complete (the check plus " +
		"the checks it needs: parse, then names/types/overloads, then " +
		"static evaluation or the cross-file checks). Today the " +
		"server publishes once, after every check; that is the " +
		"\"published\" row. Self is the check's own time. Overloads " +
		"run inside the names phase; their self time is summed over " +
		"the job workers. Body edits land in an architecture or a " +
		"package body, signature edits in a primary unit (an entity " +
		"or a package) whose declarations other units see.\n\n")
	var sim *result
	for i, r := range rs {
		if r.SimOnly {
			sim = &rs[i]
		}
	}
	for _, r := range rs {
		if r.SimOnly {
			fmt.Fprintf(&b, "### %s (%d files; simulation only)\n\n",
				r.Project, r.Files)
			simTable(&b, r)
			continue
		}
		body := ofKind(r.Warm.Samples, KindBody)
		sig := ofKind(r.Warm.Samples, KindSignature)
		fmt.Fprintf(&b, "### %s (%d files; %d cold, %d+%d warm samples)"+
			"\n\n", r.Project, r.Files, len(r.Cold), len(body), len(sig))
		b.WriteString("| Check | Cold p95 ready | Body p50 ready | " +
			"Body p95 ready | Body p95 self | Signature p50 ready | " +
			"Signature p95 ready |\n|---|---|---|---|---|---|---|\n")
		for _, c := range Checks {
			ready := func(s Sample) float64 { return s.Ready[c] }
			self := func(s Sample) float64 { return s.Self[c] }
			fmt.Fprintf(&b, "| %s | %s | %s | %s | %s | %s | %s |\n", c,
				fmtMS(p95(col(r.Cold, ready))),
				fmtMS(p50(col(body, ready))),
				fmtMS(p95(col(body, ready))),
				fmtMS(p95(col(body, self))),
				fmtMS(p50(col(sig, ready))),
				fmtMS(p95(col(sig, ready))))
		}
		pub := func(s Sample) float64 { return s.Publish }
		fmt.Fprintf(&b, "| published | %s | %s | %s | | %s | %s |\n\n",
			fmtMS(p95(col(r.Cold, pub))),
			fmtMS(p50(col(body, pub))), fmtMS(p95(col(body, pub))),
			fmtMS(p50(col(sig, pub))), fmtMS(p95(col(sig, pub))))
		if r.WarmPeakRSSMB > 0 {
			fmt.Fprintf(&b, "Peak RSS of the warm session: %.0f MB.\n\n",
				r.WarmPeakRSSMB)
		}
		simTable(&b, r)
		if r.Warm.Reused > 0 {
			fmt.Fprintf(&b, "\n%d warm keystrokes restored a text "+
				"whose analysis the server still held, so no check "+
				"ran; they are left out above.\n", r.Warm.Reused)
		}
		if r.Warm.Skipped > 0 || len(r.Notes) > 0 {
			fmt.Fprintf(&b, "\nSkipped samples: %d. %s\n",
				r.Warm.Skipped, strings.Join(dedup(r.Notes), "; "))
		}
		b.WriteString("\n")
	}
	for _, r := range rs {
		if r.Project != "large" {
			continue
		}
		b.WriteString("### Tiers (warm p95 ready on the large " +
			"project, 50 ms rule)\n\n| Check | Body edits p95 | Tier | " +
			"Signature edits p95 | Tier |\n|---|---|---|---|---|\n")
		for _, c := range Checks {
			ready := func(s Sample) float64 { return s.Ready[c] }
			x := p95(col(ofKind(r.Warm.Samples, KindBody), ready))
			y := p95(col(ofKind(r.Warm.Samples, KindSignature), ready))
			fmt.Fprintf(&b, "| %s | %s | %s | %s | %s |\n", c, fmtMS(x),
				Tier(x), fmtMS(y), Tier(y))
		}
		b.WriteString("\n### #25 gates (large project)\n\n" +
			"| Gate | p95 | Result |\n|---|---|---|\n")
		missed := 0
		for _, g := range Gates(r, sim) {
			res := "pass"
			if !g.Pass() {
				res = "MISS"
				missed++
			}
			fmt.Fprintf(&b, "| %s | %s | %s |\n", g.Name,
				fmtMS(g.P95MS), res)
		}
		if missed > 0 {
			fmt.Fprintf(&b, "\n%d gate(s) missed: by #19 the on-disk "+
				"cache #37 moves into v1.0.\n", missed)
		} else {
			b.WriteString("\nEvery gate holds.\n")
		}
	}
	return b.String()
}

func dedup(xs []string) []string {
	var out []string
	seen := map[string]bool{}
	for _, x := range xs {
		if !seen[x] {
			seen[x] = true
			out = append(out, x)
		}
	}
	return out
}

// simTable renders the latency measures other than keystrokes.
func simTable(b *strings.Builder, r result) {
	b.WriteString("| Measure | p50 | p95 | n |\n|---|---|---|---|\n")
	for _, m := range []struct {
		name string
		xs   []float64
	}{
		{"First diagnostics from cold start", r.First},
		{"Hover (warm)", r.Warm.Hover},
		{"Definition (warm)", r.Warm.Definition},
		{"Completion (warm)", r.Warm.Completion},
		{"Semantic tokens, full file (warm)", r.Warm.Tokens},
		{"Inlay hints, full file (warm)", r.Warm.Hints},
		{"Diagnostics after save (warm)", r.Warm.Save},
		{"Run command to progress begin", r.Sim.SimBegin},
		{"Run command to \"elaborated\"", r.Sim.SimElab},
		{"Run command to first simulated-time report (or answer)",
			r.Sim.SimKernel},
	} {
		if len(m.xs) == 0 {
			continue
		}
		fmt.Fprintf(b, "| %s | %s | %s | %d |\n", m.name,
			fmtMS(p50(m.xs)), fmtMS(p95(m.xs)), len(m.xs))
	}
	if r.SimOnly && len(r.Notes) > 0 {
		fmt.Fprintf(b, "\n%s\n", strings.Join(dedup(r.Notes), "; "))
	}
	b.WriteString("\n")
}
