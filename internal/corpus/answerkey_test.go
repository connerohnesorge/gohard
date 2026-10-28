package corpus

import (
	"errors"
	"testing"

	"github.com/connerohnesorge/gohard/v2/internal/trace"
	"github.com/connerohnesorge/gohard/v2/internal/trace/tracetest"
)

// TestAnswerKey: a trace marked non-reproducible (--real-env, #18) is
// never accepted as a golden, whatever else it holds; every other
// canonical trace is, and a non-canonical one is an error.
func TestAnswerKey(t *testing.T) {
	tr := tracetest.Sample()
	plain := trace.Encode(tr)
	if err := AnswerKey(plain); err != nil {
		t.Fatalf("reproducible trace refused: %v", err)
	}
	tr.Header.NonReproducible = true
	if err := AnswerKey(trace.Encode(tr)); !errors.Is(err,
		ErrNonReproducible) {
		t.Errorf("non-reproducible trace: %v, want ErrNonReproducible",
			err)
	}
	if err := answerKey(tr); !errors.Is(err, ErrNonReproducible) {
		t.Errorf("Execute's check: %v", err)
	}
	if err := AnswerKey(append([]byte("x"), plain...)); err == nil ||
		errors.Is(err, ErrNonReproducible) {
		t.Errorf("non-canonical trace: %v", err)
	}
}
