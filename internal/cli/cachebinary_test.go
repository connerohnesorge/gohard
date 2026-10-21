package cli_test

import (
	"bytes"
	"testing"

	"github.com/connerohnesorge/gohard/internal/cli"
	"github.com/connerohnesorge/gohard/internal/env/simenv"
)

// TestCacheBinaryIdentity (#37): the executable's identity is part of
// the whole-cache key, so two builds with the same version and commit
// (and size and time) but different code never share entries: a run
// of another build wipes the cache and writes it again, and going back
// to the first build does the same. An executable that cannot be read
// runs without the cache. The identity is computed once, and only by
// commands that open the cache.
func TestCacheBinaryIdentity(t *testing.T) {
	t.Cleanup(func() { cli.SetBinary(func() string { return "" }) })
	files := map[string][]byte{
		"/work/.git/HEAD": []byte("ref: refs/heads/main\n"),
		"/work/top.vhd":   []byte("entity top is\nend entity top;\n"),
	}
	const index = "/work/.gohard/cache/index"
	sim := simenv.New(simenv.Options{Dir: "/work", Files: files})
	run := func(id string) (calls int, idx []byte) {
		t.Helper()
		cli.SetBinary(func() string { calls++; return id })
		for range 2 {
			if code, out, errb := runIn(sim, nil, "check"); code != 0 {
				t.Fatalf("%s: exit %d\n%s%s", id, code, out, errb)
			}
		}
		b, err := sim.FS.ReadFile(index)
		if err != nil {
			return calls, nil
		}
		return calls, b
	}
	calls, a := run("build-a")
	if a == nil || calls != 1 {
		t.Fatalf("build a: index %q, %d identity calls, want 1", a,
			calls)
	}
	if _, b := run("build-b"); b == nil || bytes.Equal(a, b) {
		t.Fatal("build b reuses the index of build a")
	}
	if _, a2 := run("build-a"); !bytes.Equal(a, a2) {
		t.Fatal("build a's index is not rebuilt the same")
	}
	sim = simenv.New(simenv.Options{Dir: "/work", Files: files})
	if _, u := run("unreadable"); u != nil {
		t.Fatal("an unreadable executable still writes the cache")
	}
	if _, err := sim.FS.Stat("/work/.gohard/cache"); err == nil {
		t.Fatal("an unreadable executable creates the cache folder")
	}
	// gohard fmt never opens the cache: the identity is not computed.
	calls = 0
	cli.SetBinary(func() string { calls++; return "x" })
	runIn(sim, nil, "fmt", "--check", "top.vhd")
	if calls != 0 {
		t.Fatalf("fmt computed the identity %d times", calls)
	}
}
