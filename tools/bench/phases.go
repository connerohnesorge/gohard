package main

import "time"

// Checks are the #19 checks in report order.
var Checks = []string{"parse", "names", "overloads", "static",
	"cross-file"}

// Sample is one keystroke-to-diagnostics measurement in milliseconds.
// Ready[c] is when check c's diagnostics are ready after the keystroke
// (the time the check and the checks it needs took, from the
// didChange); Self[c] is the check's own time. Publish is the time to
// the published diagnostics (every check runs before the one publish).
type Sample struct {
	Ready, Self map[string]float64
	Publish     float64
}

func ms(d time.Duration) float64 { return float64(d) / 1e6 }

// split derives a Sample from the probe events of the analysis that
// produced the diagnostics published at pub for the keystroke sent at
// t0: the last complete analysis ("parse" to "analyzed") in [t0, pub].
// ok is false when there is none (a result reused from memory).
//
// Phases: parse (planning and the parse slices, including the wait in
// the server queue), units (names, types and overloads of every unit;
// Self["overloads"] is the overload-resolution part summed over the
// job workers), cross-file (the cross-unit checks and the elaboration
// checks of internal/elab/hier) and static (static evaluation).
func split(evs []event, t0, pub time.Time) (Sample, bool) {
	end := -1
	for i, e := range evs {
		if e.name == "analyzed" && !e.at.Before(t0) && !e.at.After(pub) {
			end = i
		}
	}
	if end < 0 {
		return Sample{}, false
	}
	at := map[string]event{}
	begin := -1
	for i := end; i >= 0 && !evs[i].at.Before(t0); i-- {
		if evs[i].name == "parse" {
			begin = i
			break
		}
	}
	if begin < 0 {
		return Sample{}, false
	}
	for _, e := range evs[begin : end+1] {
		at[e.name] = e
	}
	for _, n := range []string{"units", "cross-file", "static", "done",
		"hierarchy"} {
		if _, ok := at[n]; !ok {
			return Sample{}, false
		}
	}
	d := func(a, b string) time.Duration { return at[b].at.Sub(at[a].at) }
	self := map[string]float64{
		"parse":     ms(at["units"].at.Sub(t0)),
		"names":     ms(d("units", "cross-file")),
		"overloads": ms(at["cross-file"].ovl),
		"static":    ms(d("static", "done")),
		"cross-file": ms(
			d("cross-file", "static") + d("hierarchy", "analyzed"),
		),
	}
	names := self["parse"] + self["names"]
	ready := map[string]float64{
		"parse":      self["parse"],
		"names":      names,
		"overloads":  names,
		"static":     names + self["static"],
		"cross-file": names + self["cross-file"],
	}
	return Sample{Ready: ready, Self: self, Publish: ms(pub.Sub(t0))}, true
}
