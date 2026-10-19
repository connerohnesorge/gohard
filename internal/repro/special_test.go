package repro

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/connerohnesorge/gohard/internal/env"
	"github.com/connerohnesorge/gohard/internal/env/realenv"
	"github.com/connerohnesorge/gohard/internal/env/simenv"
)

// specialFS reports chosen names as a FIFO, a character device (what a
// link to /dev/zero stats as) or a file over the size limit, and fails
// the test if anything reads them: a real read would block forever or
// never end. kinds is keyed by slash paths; env.FS names are OS paths
// (Verify joins with filepath.Join, so they hold `\` on Windows), and
// lookups normalize them the way simenv does.
type specialFS struct {
	env.FS
	t     *testing.T
	kinds map[string]specialInfo
}

type specialInfo struct {
	mode fs.FileMode
	size int64
}

func (s specialFS) Stat(name string) (fs.FileInfo, error) {
	if k, ok := s.kinds[filepath.ToSlash(name)]; ok {
		return k.info(name), nil
	}
	return s.FS.Stat(name)
}

func (s specialFS) ReadFile(name string) ([]byte, error) {
	if _, ok := s.kinds[filepath.ToSlash(name)]; ok {
		s.t.Fatalf("ReadFile(%s): a special or oversize file was read",
			name)
	}
	return s.FS.ReadFile(name)
}

func (k specialInfo) info(name string) fs.FileInfo {
	return fakeInfo{name: filepath.Base(name), mode: k.mode, size: k.size}
}

type fakeInfo struct {
	name string
	mode fs.FileMode
	size int64
}

func (f fakeInfo) Name() string       { return f.name }
func (f fakeInfo) Size() int64        { return f.size }
func (f fakeInfo) Mode() fs.FileMode  { return f.mode }
func (f fakeInfo) ModTime() time.Time { return time.Time{} }
func (f fakeInfo) IsDir() bool        { return f.mode.IsDir() }
func (f fakeInfo) Sys() any           { return nil }

var specialKinds = []struct {
	name string
	info specialInfo
}{
	{"fifo", specialInfo{mode: fs.ModeNamedPipe | 0o644}},
	{"device", specialInfo{mode: fs.ModeDevice | fs.ModeCharDevice |
		0o666}},
	{"oversize", specialInfo{mode: 0o644, size: MaxFileSize + 1}},
	{"directory", specialInfo{mode: fs.ModeDir | 0o755}},
}

// TestVerifySpecialSources: a recorded source (or gohard.yaml) that is
// now a FIFO, a device, a directory or too large is never read; Verify
// reports it as changed (repro/source-changed, exit 2) and HashFiles
// fails, instead of hanging or reading without end.
func TestVerifySpecialSources(t *testing.T) {
	for _, k := range specialKinds {
		for _, victim := range []string{"src/a.vhd", ConfigFile} {
			t.Run(k.name+"/"+victim, func(t *testing.T) {
				s := project(t, simenv.Faults{}, 0)
				h := recordHeader(t, s.Env().FS)
				fsys := specialFS{FS: s.Env().FS, t: t,
					kinds: map[string]specialInfo{
						"/proj/" + victim: k.info,
					}}
				c, err := Verify(fsys, "/proj", h, h.Version)
				if err != nil {
					t.Fatalf("Verify: %v", err)
				}
				if len(c.Changed) != 1 || c.Changed[0] != victim {
					t.Fatalf("Changed = %q, want [%s]", c.Changed,
						victim)
				}
				if victim == ConfigFile {
					if _, err := HashConfig(fsys, "/proj"); err == nil {
						t.Fatal("HashConfig of a special file succeeded")
					}
					return
				}
				_, err = HashFiles(fsys, "/proj", []string{victim})
				if !errors.Is(err, ErrNotRegular) &&
					!errors.Is(err, ErrTooLarge) {
					t.Fatalf("HashFiles err = %v", err)
				}
			})
		}
	}
}

// TestVerifyDevZeroLink runs Verify on the real file system with a
// recorded source that is now a symbolic link to /dev/zero.
func TestVerifyDevZeroLink(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("needs /dev/zero")
	}
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("/dev/zero",
		filepath.Join(dir, "src", "a.vhd")); err != nil {
		t.Fatal(err)
	}
	fsys := realenv.New(realenv.Options{Dir: dir}).FS
	h := sample()
	h.Config = nil
	h.Sources = []File{{Path: "src/a.vhd", SHA256: Hash([]byte("x"))}}
	c, err := Verify(fsys, dir, h, h.Version)
	if err != nil || len(c.Changed) != 1 || c.Changed[0] != "src/a.vhd" {
		t.Fatalf("Verify = %+v, %v", c, err)
	}
	if _, err := HashFiles(fsys, dir, []string{"src/a.vhd"}); !errors.Is(
		err, ErrNotRegular) {
		t.Fatalf("HashFiles err = %v", err)
	}
}
