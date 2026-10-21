package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestBinaryIdentity: the build identity of the analysis cache key
// (#37) is a hash of the executable's content, so files of the same
// size and modification time (as in the nix store, mtime 1) but other
// content get other identities, the same content the same one, and a
// file that cannot be read the identity that turns the cache off.
func TestBinaryIdentity(t *testing.T) {
	dir := t.TempDir()
	write := func(name string, b []byte) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, b, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(p, time.Unix(1, 0),
			time.Unix(1, 0)); err != nil {
			t.Fatal(err)
		}
		return p
	}
	a := write("a", []byte("is not declared in"))
	b := write("b", []byte("is not daclared in"))
	a2 := write("a2", []byte("is not declared in"))
	ia, ib, ia2 := binaryIdentity(a), binaryIdentity(b),
		binaryIdentity(a2)
	if ia == ib {
		t.Fatalf("same size and time, other content: both %s", ia)
	}
	if ia != ia2 {
		t.Fatalf("same content: %s and %s", ia, ia2)
	}
	if ia == "unreadable" || len(ia) != 64 {
		t.Fatalf("identity %q, want a hex SHA-256", ia)
	}
	if got := binaryIdentity(filepath.Join(dir, "none")); got !=
		"unreadable" {
		t.Fatalf("missing file: %q", got)
	}
}
