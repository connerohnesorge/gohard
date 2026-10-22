package corpus

import (
	"errors"
	"fmt"

	"github.com/connerohnesorge/gohard/internal/trace"
)

// ErrNonReproducible is the error of a trace recorded with --real-env
// (#18): it read the real clock or environment, so it can never be a
// corpus answer key.
var ErrNonReproducible = errors.New("the trace is marked " +
	"non-reproducible (recorded with --real-env) and cannot be an " +
	"answer key")

// AnswerKey checks that data, a canonical trace, may be a corpus
// golden: it decodes and is not marked non-reproducible.
func AnswerKey(data []byte) error {
	t, err := trace.Decode(data)
	if err != nil {
		return fmt.Errorf("golden trace: %w", err)
	}
	return answerKey(t)
}

func answerKey(t *trace.Trace) error {
	if t.Header.NonReproducible {
		return ErrNonReproducible
	}
	return nil
}
