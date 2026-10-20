// Package fuzzselftest holds FuzzSeededCrash, a fuzz target that fails
// on purpose so the nightly crash flow (#30) can be proven on demand:
// dispatching nightly.yml with seed_crash=true adds it to the fuzz
// matrix, and scripts/ci/fuzz.sh minimizes its crash, commits
// testdata/fuzz/FuzzSeededCrash/<id> on a branch and opens the pull
// request.
//
// It skips unless GOHARD_FUZZ_SEEDED_CRASH=1, which only the seeded
// nightly run sets, so plain go test ./... never fails here. No corpus
// is saved under testdata/fuzz/FuzzSeededCrash; never merge one.
package fuzzselftest

import (
	"bytes"
	"os"
	"testing"
)

// envSeededCrash enables FuzzSeededCrash.
const envSeededCrash = "GOHARD_FUZZ_SEEDED_CRASH"

func FuzzSeededCrash(f *testing.F) {
	if os.Getenv(envSeededCrash) != "1" {
		f.Skipf("set %s=1 to run the seeded crash target", envSeededCrash)
	}
	// One byte short of the crash: the seed itself passes (a failing
	// seed writes no input), and the fuzzer finds "crash" in seconds.
	f.Add([]byte("cras"))
	f.Fuzz(func(t *testing.T, in []byte) {
		if bytes.HasPrefix(in, []byte("crash")) {
			t.Fatalf("seeded crash: input %q starts with \"crash\"", in)
		}
	})
}
