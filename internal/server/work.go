package server

import (
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	"github.com/connerohnesorge/gohard/internal/jsonrpc2"
)

// work is one running task: a request or the diagnostics of a
// snapshot. Like every part of State it is a value; a slice replaces
// it.
type work struct {
	id     uint64
	isReq  bool // false for diagnostics
	reqID  jsonrpc2.ID
	method string
	snap   uint64 // the snapshot the task computes for
	run    runner
	since  time.Duration // virtual time of the last progress
	// token is the $/progress token: the request's workDoneToken, or
	// one the server created; tokenReady says it may be used.
	token      json.RawMessage
	tokenReady bool
	begun      bool
	// parked is a request waiting for the project files; edits is
	// the edit count when it arrived.
	parked *call
	edits  uint64
	// sim marks simulation work (sim.go).
	sim *simJob
}

// runner is a Task with its result converted to the value the server
// sends.
type runner interface {
	step(budget int) (next runner, result any, done bool, rep *Report)
}

type taskRunner[T any] struct {
	t    Task[T]
	conv func(T) any
}

func (r taskRunner[T]) step(budget int) (runner, any, bool, *Report) {
	sl := r.t.Run(budget)
	if sl.Done || sl.Next == nil {
		return nil, r.conv(sl.Value), true, sl.Report
	}
	return taskRunner[T]{sl.Next, r.conv}, nil, false, sl.Report
}

func runTask[T any](t Task[T], conv func(T) any) runner {
	return taskRunner[T]{t, conv}
}

func (s *State) setWork(w work) { s.work = cloneSet(s.work, w.id, w) }

// startWork queues the first slice of a new work item.
func (s *State) startWork(w work) work {
	if w.id == 0 {
		s.nextWork++
		w.id = s.nextWork
	}
	w.snap = s.snap.ID
	w.since = s.now
	w.tokenReady = w.token != nil
	s.setWork(w)
	if w.isReq {
		s.byRequest = cloneSet(s.byRequest, w.reqID.String(), w.id)
	}
	*s = s.Push(Work{ID: w.id}, w.pri())
	return w
}

// dropWork removes a work item without a response.
func (s *State) dropWork(id uint64) {
	w, ok := s.work[id]
	if !ok {
		return
	}
	s.work = cloneDelete(s.work, id)
	if w.isReq {
		s.byRequest = cloneDelete(s.byRequest, w.reqID.String())
	}
	if s.diagWork == id {
		s.diagWork = 0
	}
}

// finish ends the progress of w and removes it.
func (s *State) finish(o *out, w work) {
	if w.begun {
		o.notify("$/progress", progressParams{Token: w.token,
			Value: progressValue{Kind: "end"}})
	}
	s.dropWork(w.id)
	if w.sim != nil && w.sim.tb != "" {
		s.refreshLenses(o)
	}
}

// slice runs one slice of work id. A request whose snapshot is no
// longer the latest gets ContentModified, and stale diagnostics are
// dropped: a result for an older snapshot is never published (P3).
func (s *State) slice(o *out, id uint64) {
	w, ok := s.work[id]
	if !ok || w.parked != nil {
		return // cancelled or superseded
	}
	if w.snap != s.snap.ID {
		s.finish(o, w)
		if w.isReq {
			s.fail(o, w.reqID, jsonrpc2.CodeContentModified,
				"the content changed while "+w.method+" ran")
		}
		return
	}
	if w.sim != nil && s.outranked(id) {
		// Editor requests and diagnostics outrank simulation.
		w.since = s.now
		s.setWork(w)
		*s = s.Push(Work{ID: w.id}, PrioritySim)
		return
	}
	next, res, finished, rep, err := safeStep(w.run)
	if err != nil {
		s.finish(o, w)
		if w.isReq {
			s.fail(o, w.reqID, jsonrpc2.CodeInternalError, err.Error())
		} else {
			s.log(o, LogError, "diagnostics: "+err.Error())
		}
		return
	}
	if rep != nil || finished {
		w.since = s.now
	}
	if finished {
		s.finish(o, w)
		if s.simFinished(o, w, res) {
			return
		}
		if f, ok := res.(failure); ok && w.isReq {
			s.fail(o, w.reqID, jsonrpc2.CodeRequestFailed, f.msg)
		} else if w.isReq {
			s.respond(o, w.reqID, res)
		} else {
			s.publishResults(o, res)
		}
		return
	}
	if s.now-w.since >= NoProgress {
		s.finish(o, w)
		msg := w.method + " made no progress for " +
			NoProgress.String() + " (" +
			strconv.FormatInt(int64(NoProgress/Tick), 10) + " steps)"
		if w.isReq {
			s.fail(o, w.reqID, jsonrpc2.CodeRequestFailed, msg)
		} else {
			s.log(o, LogError, msg)
		}
		return
	}
	w.run = next
	s.progress(o, &w, rep)
	s.setWork(w)
	*s = s.Push(Work{ID: w.id}, w.pri())
}

// safeStep runs one slice and turns a panic in the analyzer into an
// error, so a bug in a feature fails one request instead of the
// server (P6).
func safeStep(r runner) (next runner, res any, finished bool,
	rep *Report, err error,
) {
	defer func() {
		if v := recover(); v != nil {
			err = fmt.Errorf("internal error: %v", v)
		}
	}()
	next, res, finished, rep = r.step(SliceBudget)
	return next, res, finished, rep, nil
}

// cancel handles $/cancelRequest. A request still running ends with
// RequestCancelled; a request that already got its response, or an
// unknown ID, is ignored, so every request gets exactly one response.
func (s *State) cancel(o *out, params json.RawMessage) {
	var p struct {
		ID *jsonrpc2.ID `json:"id"`
	}
	if json.Unmarshal(params, &p) != nil || p.ID == nil {
		return
	}
	id, ok := s.byRequest[p.ID.String()]
	if !ok {
		return
	}
	w := s.work[id]
	s.finish(o, w)
	s.fail(o, w.reqID, jsonrpc2.CodeRequestCancelled, "cancelled")
}

type progressParams struct {
	Token json.RawMessage `json:"token"`
	Value progressValue   `json:"value"`
}

type progressValue struct {
	Kind       string `json:"kind"`
	Title      string `json:"title,omitempty"`
	Message    string `json:"message,omitempty"`
	Percentage *int   `json:"percentage,omitempty"`
}

// progress reports a running task through $/progress (#25). A request
// uses the client's workDoneToken; diagnostics use a token the server
// creates with window/workDoneProgress/create when the client supports
// it, and begin only after the client accepted it.
func (s *State) progress(o *out, w *work, rep *Report) {
	if w.token == nil && (!w.isReq || w.sim != nil) && s.progressOK {
		s.nextOut++
		w.token = marshal("gohard/" + strconv.FormatInt(s.nextOut, 10))
		s.creating = cloneSet(s.creating, s.nextOut, w.id)
		o.send(&jsonrpc2.Request{ID: jsonrpc2.IntID(s.nextOut),
			Method: "window/workDoneProgress/create",
			Params: marshal(struct {
				Token json.RawMessage `json:"token"`
			}{w.token})})
		return
	}
	if !w.tokenReady {
		return
	}
	if !w.begun {
		s.beginProgress(o, w)
	}
	if rep != nil {
		v := progressValue{Kind: "report", Message: rep.Message}
		if rep.Percentage >= 0 {
			v.Percentage = &rep.Percentage
		}
		o.notify("$/progress", progressParams{Token: w.token, Value: v})
	}
}

func (s *State) beginProgress(o *out, w *work) {
	if w.begun || w.token == nil {
		return
	}
	w.begun = true
	title := w.method
	switch {
	case w.sim != nil && w.sim.tb != "":
		title = "gohard: simulating " + w.sim.tb
	case w.sim != nil:
		title = "gohard: simulation"
	case !w.isReq:
		title = "gohard: checking"
	}
	o.notify("$/progress", progressParams{Token: w.token,
		Value: progressValue{Kind: "begin", Title: title}})
}
