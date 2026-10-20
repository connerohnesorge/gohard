package server

import "slices"

// startDiagnostics replaces the running diagnostics with a check of
// the latest snapshot. The older run is dropped unpublished.
func (s *State) startDiagnostics(o *out) {
	if !s.loaded || s.phase != phaseRunning ||
		!s.an.Features().Has(FeatureDiagnostics) {
		return
	}
	if w, ok := s.work[s.diagWork]; ok {
		s.finish(o, w)
	}
	conv := func(fds []FileDiagnostics) any { return fds }
	w := s.startWork(work{method: "diagnostics",
		run: runTask(s.an.Diagnostics(s.snap), conv)})
	s.diagWork = w.id
}

// publishResults publishes finished diagnostics of the latest
// snapshot: every document with diagnostics, sorted by URI, and an
// empty list for every document that had diagnostics before and has
// none now.
func (s *State) publishResults(o *out, res any) {
	s.anDiags, _ = res.([]FileDiagnostics)
	s.publishAll(o)
}

// publishAll publishes the analysis diagnostics with the gohard-sim
// diagnostics of the last runs after them.
func (s *State) publishAll(o *out) {
	byURI := map[string][]lspDiagnostic{}
	for _, fd := range s.anDiags {
		d, ok := s.snap.Document(fd.URI)
		if !ok {
			continue
		}
		for _, dg := range fd.Diagnostics {
			byURI[d.URI] = append(byURI[d.URI],
				s.toLSPDiagnostic(d.index, dg))
		}
	}
	sim := s.simDiagnostics()
	for _, uri := range sortedKeys(sim) {
		byURI[uri] = append(byURI[uri], sim[uri]...)
	}
	now := sortedKeys(byURI)
	for _, uri := range now {
		d, _ := s.snap.Document(uri)
		var version *int32
		if d.Open {
			version = &d.Version
		}
		s.publish(o, uri, version, byURI[uri])
	}
	for _, uri := range s.published {
		if _, ok := slices.BinarySearch(now, uri); !ok {
			s.publish(o, uri, nil, nil)
		}
	}
	s.published = now
}

func (s *State) publish(o *out, uri string, version *int32,
	ds []lspDiagnostic,
) {
	if ds == nil {
		ds = []lspDiagnostic{}
	}
	o.notify("textDocument/publishDiagnostics", struct {
		URI         string          `json:"uri"`
		Version     *int32          `json:"version,omitempty"`
		Diagnostics []lspDiagnostic `json:"diagnostics"`
	}{uri, version, ds})
	o.notify(SnapshotMethod, struct {
		Snapshot uint64 `json:"snapshot"`
		URI      string `json:"uri"`
	}{s.snap.ID, uri})
}
