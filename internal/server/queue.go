package server

import (
	"cmp"
	"encoding/json"
	"slices"
	"time"

	"github.com/connerohnesorge/gohard/internal/config"
	"github.com/connerohnesorge/gohard/internal/diag"
	"github.com/connerohnesorge/gohard/internal/jsonrpc2"
	"github.com/connerohnesorge/gohard/internal/workspace"
)

// Priority orders events that are due at the same virtual time.
type Priority uint8

// Priorities, most urgent first.
const (
	// PriorityResult is the result of an effect the core asked for.
	PriorityResult Priority = iota
	// PriorityClient is a client message; client messages keep their
	// arrival order.
	PriorityClient
	// PrioritySlice is one slice of background work.
	PrioritySlice
)

// Event is one input of Step. The queue orders events by At, then Pri,
// then Seq (P3).
type Event struct {
	// At is the virtual time the event is due.
	At time.Duration
	// Pri breaks ties at the same time.
	Pri Priority
	// Seq is assigned by Queue.Push and breaks the remaining ties in
	// push order.
	Seq  uint64
	Body Body
}

// Body is the payload of an Event: Message, Rejected, Closed,
// ConfigLoaded, WorkspaceLoaded or Work.
type Body interface{ body() }

// Message is one JSON-RPC message from the client, as read from the
// transport.
type Message struct{ Data json.RawMessage }

// Rejected is a client request the transport skipped without reading
// it (a *jsonrpc2.SkippedError that kept the request ID: a body over
// the size limit or in a charset other than UTF-8). The core answers it
// with InvalidRequest, so the request still gets its one response
// (#25).
type Rejected struct {
	ID     jsonrpc2.ID
	Reason string
}

// Closed reports that the client's stream ended (end of file) without
// an exit notification.
type Closed struct{}

// ConfigLoaded is the result of a LoadConfig effect.
type ConfigLoaded struct{ Loaded config.Loaded }

// WorkspaceLoaded is the result of a LoadWorkspace effect: every VHDL
// file of the project with its text, sorted by path.
type WorkspaceLoaded struct {
	Root        string
	Sources     []workspace.Source
	Diagnostics []diag.Diagnostic
}

// Work runs the next slice of the work item ID.
type Work struct{ ID uint64 }

func (Message) body()         {}
func (Rejected) body()        {}
func (Closed) body()          {}
func (ConfigLoaded) body()    {}
func (WorkspaceLoaded) body() {}
func (Work) body()            {}

// Queue is the event queue. It is a value: Push and Pop return a new
// queue and never change the receiver's items, so copies of a State
// never share pending events.
type Queue struct {
	items []Event // sorted by (At, Pri, Seq)
	seq   uint64
}

// Len returns the number of pending events.
func (q Queue) Len() int { return len(q.items) }

// Push adds ev, assigning the next sequence number.
func (q Queue) Push(ev Event) Queue {
	q.seq++
	ev.Seq = q.seq
	i, _ := slices.BinarySearchFunc(q.items, ev, compareEvents)
	items := make([]Event, 0, len(q.items)+1)
	items = append(items, q.items[:i]...)
	items = append(items, ev)
	q.items = append(items, q.items[i:]...)
	return q
}

// Peek returns the first event without removing it.
func (q Queue) Peek() (Event, bool) {
	if len(q.items) == 0 {
		return Event{}, false
	}
	return q.items[0], true
}

// Pop removes and returns the first event.
func (q Queue) Pop() (Queue, Event, bool) {
	if len(q.items) == 0 {
		return q, Event{}, false
	}
	ev := q.items[0]
	q.items = slices.Clone(q.items[1:])
	return q, ev, true
}

func compareEvents(a, b Event) int {
	return cmp.Or(cmp.Compare(a.At, b.At), cmp.Compare(a.Pri, b.Pri),
		cmp.Compare(a.Seq, b.Seq))
}

// Effect is an action the shell performs for the core.
type Effect interface{ effect() }

// LoadConfig asks the shell to run config.Load from Folder and to
// push the result as ConfigLoaded.
type LoadConfig struct{ Folder string }

// LoadWorkspace asks the shell to discover and read every VHDL file
// under Root with workspace.Discover and workspace.Read and to push the
// result as WorkspaceLoaded.
type LoadWorkspace struct{ Root string }

// Exit ends the session with Code (0 after shutdown, else 1).
type Exit struct{ Code int }

func (Send) effect()          {}
func (LoadConfig) effect()    {}
func (LoadWorkspace) effect() {}
func (Exit) effect()          {}
