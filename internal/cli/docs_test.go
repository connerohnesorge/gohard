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

// TestCIExampleJUnit pins the documented JUnit examples (#61): GitLab
// reads the file through artifacts:reports:junit even when the job
// fails, and the GitHub Actions report step runs after a failure too.
func TestCIExampleJUnit(t *testing.T) {
	raw, err := os.ReadFile("../../docs/ci.md")
	if err != nil {
		t.Fatal(err)
	}
	doc := strings.ReplaceAll(string(raw), "\r\n", "\n")
	for _, want := range []string{
		"gohard check --sim --format junit --output gohard-junit.xml",
		"    when: always\n",
		"    reports:\n      junit: gohard-junit.xml\n",
		"      - if: always()\n        uses: mikepenz/action-junit-report",
		"          report_paths: gohard-junit.xml\n",
	} {
		if !strings.Contains(doc, want) {
			t.Errorf("docs/ci.md lacks %q", want)
		}
	}
}

// TestInstallPathsListed pins the install docs (#69): the README
// names every path of the docs/install.md table with the same command
// or link, and the tap and bucket the commands name are the
// repositories .goreleaser.yaml pushes the cask and Scoop manifest to.
func TestInstallPathsListed(t *testing.T) {
	read := func(path string) string {
		raw, err := os.ReadFile("../../" + path)
		if err != nil {
			t.Fatal(err)
		}
		return strings.ReplaceAll(string(raw), "\r\n", "\n")
	}
	// flat collapses whitespace: the README wraps commands.
	flat := func(s string) string {
		return strings.Join(strings.Fields(s), " ")
	}
	install := read("docs/install.md")
	readme := flat(read("README.md"))
	release := flat(read(".goreleaser.yaml"))
	var paths []string
	for _, l := range strings.Split(install, "\n") {
		cells := strings.Split(l, " | ")
		if !strings.HasPrefix(l, "| ") || len(cells) != 3 ||
			cells[0] == "| Path" || cells[1] == "---" {
			continue
		}
		paths = append(paths, strings.TrimPrefix(cells[0], "| "))
		// The command column: its code spans, or else its link.
		want := strings.Split(cells[1], "`")
		if len(want) == 1 {
			_, link, _ := strings.Cut(cells[1], "](")
			link, _, _ = strings.Cut(link, ")")
			want = []string{"", link}
		}
		for i := 1; i < len(want); i += 2 {
			if !strings.Contains(readme, flat(want[i])) {
				t.Errorf("README.md lacks %s install %q", paths[len(paths)-1],
					want[i])
			}
		}
	}
	if got := strings.Join(paths, ", "); got != "Homebrew, Scoop, "+
		"nixpkgs, Nix flake, Go, Release archive" {
		t.Errorf("docs/install.md table lists %s", got)
	}
	for _, link := range []struct{ cmd, repo string }{
		{"brew install --cask connerohnesorge/tap/gohard",
			"owner: connerohnesorge name: homebrew-tap"},
		{"scoop bucket add connerohnesorge " +
			"https://github.com/connerohnesorge/scoop-bucket",
			"owner: connerohnesorge name: scoop-bucket"},
	} {
		if !strings.Contains(flat(install), link.cmd) {
			t.Errorf("docs/install.md lacks %q", link.cmd)
		}
		if !strings.Contains(release, link.repo) {
			t.Errorf(".goreleaser.yaml does not push to %q", link.repo)
		}
	}
}

// TestReleaseAttestsAfterPublishError pins the attest step of the
// release workflow (#69). goreleaser publishes the GitHub release
// before it pushes the Homebrew cask and the Scoop manifest, and a
// failed push still makes it exit non-zero, so the attest step must
// not depend on goreleaser's exit status: it runs whenever goreleaser
// wrote dist/checksums.txt, unless the run was cancelled.
func TestReleaseAttestsAfterPublishError(t *testing.T) {
	raw, err := os.ReadFile("../../.github/workflows/release.yml")
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.ReplaceAll(string(raw), "\r\n", "\n"),
		"\n")
	// The steps are list items at one indentation; collect the one
	// that uses actions/attest.
	const item = "      - "
	var step []string
	for i, l := range lines {
		if !strings.Contains(l, "uses: actions/attest@") {
			continue
		}
		start := i
		for start > 0 && !strings.HasPrefix(lines[start], item) {
			start--
		}
		end := i + 1
		for end < len(lines) && !strings.HasPrefix(lines[end], item) {
			end++
		}
		step = lines[start:end]
		break
	}
	if step == nil {
		t.Fatal("release.yml: no actions/attest step")
	}
	var guard string
	for _, l := range step {
		if f := strings.TrimPrefix(strings.TrimSpace(l), "- "); strings.
			HasPrefix(f, "if:") {
			guard = f
		}
	}
	for _, want := range []string{"!cancelled()",
		"hashFiles('dist/checksums.txt') != ''"} {
		if !strings.Contains(guard, want) {
			t.Errorf("attest step guard %q lacks %q", guard, want)
		}
	}
}
