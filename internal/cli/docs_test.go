package cli_test

import (
	"os"
	"strings"
	"testing"
)

// TestCIExampleSkipsSARIFOnExit2 pins the documented CI example (#20):
// the SARIF upload step runs only after exit 0 or 1, so an exit-2 run
// (whose SARIF holds only the setup failure) never wipes the existing
// code-scanning alerts, and the job still fails on exit 2.
func TestCIExampleSkipsSARIFOnExit2(t *testing.T) {
	raw, err := os.ReadFile("../../docs/ci.md")
	if err != nil {
		t.Fatal(err)
	}
	doc := strings.ReplaceAll(string(raw), "\r\n", "\n")
	if !strings.Contains(doc, "gohard check --format sarif") {
		t.Fatal("docs/ci.md: no gohard check --format sarif step")
	}
	lines := strings.Split(doc, "\n")
	upload := -1
	for i, l := range lines {
		if strings.Contains(l, "upload-sarif") {
			upload = i
			break
		}
	}
	if upload < 1 {
		t.Fatal("docs/ci.md: no upload-sarif step")
	}
	guard := lines[upload-1]
	for _, want := range []string{"- if:", "exit == '0'",
		"exit == '1'"} {
		if !strings.Contains(guard, want) {
			t.Fatalf("upload-sarif guard %q lacks %q", guard, want)
		}
	}
	if strings.Contains(guard, "'2'") {
		t.Fatalf("upload-sarif guard %q uploads on exit 2", guard)
	}
	if !strings.Contains(doc, "steps.gohard.outputs.exit != '0'") {
		t.Fatal("docs/ci.md: the job does not fail on a non-zero exit")
	}
}
