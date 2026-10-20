package corpus

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestMissingSubmoduleRoot: on a real checkout root that holds only
// corpus.yaml (the state of a clone whose submodules were never
// initialised), every run of every design and every regression entry
// fails with a message naming git submodule update --init; nothing is
// skipped (#8, #30).
func TestMissingSubmoduleRoot(t *testing.T) {
	src, err := os.ReadFile(filepath.Join(repoRoot(t),
		filepath.FromSlash(ManifestPath)))
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	dst := filepath.Join(root, filepath.FromSlash(ManifestPath))
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dst, src, 0o644); err != nil {
		t.Fatal(err)
	}
	m, err := ParseManifest(src)
	if err != nil {
		t.Fatal(err)
	}
	read := reader(root)
	runs := 0
	for _, d := range m.Designs {
		for _, r := range d.Runs() {
			runs++
			_, err := r.Sources(read)
			if err == nil || !strings.Contains(err.Error(),
				SubmoduleHint) {
				t.Errorf("%s: err = %v", r.Name(), err)
			}
		}
	}
	if runs == 0 || len(m.Regression) == 0 {
		t.Fatalf("%d runs, %d regression entries", runs,
			len(m.Regression))
	}
	for _, r := range m.Regression {
		_, err := os.Stat(filepath.Join(root, filepath.FromSlash(r.Path)))
		if err == nil {
			t.Fatalf("regression %s present in empty root", r.Name)
		}
		if msg := Missing(r.Path, err).Error(); !strings.Contains(msg,
			SubmoduleHint) {
			t.Errorf("regression %s: %s", r.Name, msg)
		}
	}
}
