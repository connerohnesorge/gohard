package cli_test

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/connerohnesorge/gohard/internal/diag/junittest"
)

// TestJUnitFixturesValid: the JUnit XML of every testdata/cli fixture
// run with --format junit (stdout, or the --output file in want/)
// validates against the committed junit-10.xsd (#61).
func TestJUnitFixturesValid(t *testing.T) {
	cases, err := os.ReadDir(fixtureRoot)
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, c := range cases {
		dir := filepath.Join(fixtureRoot, c.Name())
		raw, err := os.ReadFile(filepath.Join(dir, "cmd"))
		if err != nil {
			continue
		}
		words, err := commandWords(string(raw))
		if err != nil {
			t.Fatalf("%s: %v", c.Name(), err)
		}
		i := slices.Index(words, "--format")
		if i < 0 || i+1 >= len(words) || words[i+1] != "junit" {
			continue
		}
		n++
		out := filepath.Join(dir, "want.stdout")
		if j := slices.Index(words, "--output"); j >= 0 &&
			j+1 < len(words) {
			out = filepath.Join(dir, "want", words[j+1])
		}
		doc, err := os.ReadFile(out)
		if err != nil {
			t.Fatalf("%s: %v", c.Name(), err)
		}
		if err := junittest.Validate(doc); err != nil {
			t.Errorf("%s: %s is not valid JUnit XML: %v", c.Name(),
				filepath.Base(out), err)
		}
	}
	if n < 10 {
		t.Fatalf("found %d JUnit fixtures, want at least 10", n)
	}
}
