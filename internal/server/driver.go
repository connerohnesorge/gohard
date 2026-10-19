package server

import (
	"github.com/connerohnesorge/gohard/internal/config"
	"github.com/connerohnesorge/gohard/internal/diag"
	"github.com/connerohnesorge/gohard/internal/env"
	"github.com/connerohnesorge/gohard/internal/workspace"
)

// Driver is the thin shell around the core (P3): it queues client
// messages, runs Step one event at a time, and performs every effect
// through the env doors. The live server, replay, the test harness and
// `gohard lsp <action>` all use it, so they share one code path.
//
// Scheduling rule: client input is queued between steps. The live
// shell (Serve) takes every message that is already waiting before it
// runs the next step, so a $/cancelRequest or an edit that arrives
// while work is running takes effect at the next slice boundary (#25).
// The number of steps run before each message was queued is part of
// the recorded session (Arrival), and Replay queues every message
// after the same number of steps, so a replay is byte-identical to the
// live session (#29). Deliver, used by actions and by sessions written
// by hand, queues a message once the core is idle.
type Driver struct {
	State  State
	env    env.Env
	jobs   int
	send   func(Send) error
	exit   int
	done   bool
	closed bool
	steps  uint64
	// loadConfig, when set, performs LoadConfig instead of config.Load
	// on the env's file system: Serve records each read and Replay
	// feeds the recorded ones back (#29).
	loadConfig func(LoadConfig) config.Loaded
}

// NewDriver returns a driver for a new server. send writes one
// message to the client; jobs is the -p limit for reading files.
func NewDriver(o Options, e env.Env, jobs int,
	send func(Send) error,
) *Driver {
	return &Driver{State: New(o), env: e, jobs: jobs, send: send}
}

// Deliver runs the core until it is idle, queues one client message (a
// JSON-RPC body) and runs the core until it is idle again. It returns
// the first error of send.
func (d *Driver) Deliver(body []byte) error {
	if err := d.Run(); err != nil {
		return err
	}
	d.Push(body)
	return d.Run()
}

// Close runs the core until it is idle and delivers the end of the
// client stream: the session ends as if the client sent exit.
func (d *Driver) Close() error {
	if err := d.Run(); err != nil {
		return err
	}
	d.PushClosed()
	return d.Run()
}

// Push queues one client message without running the core.
func (d *Driver) Push(body []byte) {
	if d.done || d.closed {
		return
	}
	d.State = d.State.Push(Message{Data: body}, PriorityClient)
}

// PushRejected queues a request the transport skipped unread (see
// Rejected) without running the core.
func (d *Driver) PushRejected(r Rejected) {
	if d.done || d.closed {
		return
	}
	d.State = d.State.Push(r, PriorityClient)
}

// PushClosed queues the end of the client stream without running the
// core. Later calls and later messages are ignored.
func (d *Driver) PushClosed() {
	if d.done || d.closed {
		return
	}
	d.closed = true
	d.State = d.State.Push(Closed{}, PriorityClient)
}

// Idle reports whether the core has nothing to do: its queue is empty
// or the session ended.
func (d *Driver) Idle() bool { return d.done || d.State.Pending() == 0 }

// Steps returns the number of events the core has run.
func (d *Driver) Steps() uint64 { return d.steps }

// Step runs one queued event and performs its effects. It does nothing
// when the driver is idle. It returns the first error of send.
func (d *Driver) Step() error {
	if d.Idle() {
		return nil
	}
	s, ev, _ := d.State.Next()
	s, fx := Step(s, ev)
	d.State = s
	d.steps++
	for _, e := range fx {
		if err := d.perform(e); err != nil {
			return err
		}
	}
	return nil
}

// Run steps the core until it is idle.
func (d *Driver) Run() error {
	for !d.Idle() {
		if err := d.Step(); err != nil {
			return err
		}
	}
	return nil
}

// RunTo steps the core until it has run n events in total or is idle.
func (d *Driver) RunTo(n uint64) error {
	for !d.Idle() && d.steps < n {
		if err := d.Step(); err != nil {
			return err
		}
	}
	return nil
}

// Exited returns the exit code once the session ended.
func (d *Driver) Exited() (code int, ok bool) { return d.exit, d.done }

func (d *Driver) perform(e Effect) error {
	switch e := e.(type) {
	case Send:
		return d.send(e)
	case LoadConfig:
		var l config.Loaded
		if d.loadConfig != nil {
			l = d.loadConfig(e)
		} else {
			l = config.Load(d.env.FS, e.Folder)
		}
		d.State = d.State.Push(ConfigLoaded{Loaded: l}, PriorityResult)
	case LoadWorkspace:
		d.State = d.State.Push(LoadFiles(d.env, d.jobs, e.Root),
			PriorityResult)
	case Exit:
		d.exit, d.done = e.Code, true
	}
	return nil
}

// LoadFiles discovers and reads every VHDL file under root, the way
// `gohard check` does.
func LoadFiles(e env.Env, jobs int, root string) WorkspaceLoaded {
	found := workspace.Discover(e.FS, workspace.Options{Root: root,
		Dir: root})
	srcs, ds := workspace.Read(e.Jobs, jobs, e.FS, found.Files, 0)
	return WorkspaceLoaded{Root: root, Sources: srcs,
		Diagnostics: diag.Normalize(append(found.Diagnostics, ds...))}
}
