package server

// Task is resumable work that the event loop runs one slice at a time
// (P3, P7). A task is an immutable value: Run returns the continuation
// instead of changing the receiver, so a State that holds tasks stays a
// copyable value and a forked State resumes independently.
//
// The server calls Run with a step budget per slice. A task counts its
// own steps (nodes visited, declarations resolved, evaluator steps) and
// returns once it used about budget of them, so cancellation and newer
// snapshots take effect at the next slice boundary.
type Task[T any] interface {
	Run(budget int) Slice[T]
}

// Slice is the outcome of one Task.Run.
type Slice[T any] struct {
	// Done reports that Value is the final result; Next is ignored.
	Done  bool
	Value T
	// Next is the continuation when Done is false. A nil Next with
	// Done false is a finished task with the zero result.
	Next Task[T]
	// Report, when set, is shown as $/progress for the work, and it
	// counts as progress for the 5 s no-progress rule.
	Report *Report
}

// Report is one progress report of a running task.
type Report struct {
	Message string
	// Percentage is 0..100, or -1 when unknown.
	Percentage int
}

// Done returns a task that finishes in its first slice with v.
func Done[T any](v T) Task[T] { return done[T]{v} }

type done[T any] struct{ v T }

func (d done[T]) Run(int) Slice[T] { return Slice[T]{Done: true, Value: d.v} }

// TaskFunc adapts a function to a Task.
type TaskFunc[T any] func(budget int) Slice[T]

// Run implements Task.
func (f TaskFunc[T]) Run(budget int) Slice[T] { return f(budget) }

// Steps returns a task that needs n slices: the first n-1 slices
// report progress and the last returns v. It is the reference slow
// task of the tests and of the liveness workload.
func Steps[T any](n int, v T) Task[T] {
	return TaskFunc[T](func(int) Slice[T] {
		if n <= 1 {
			return Slice[T]{Done: true, Value: v}
		}
		return Slice[T]{Next: Steps(n-1, v)}
	})
}
