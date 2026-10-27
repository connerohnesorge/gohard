package stdlib

import (
	"errors"
	"path/filepath"
	"testing"

	"github.com/connerohnesorge/gohard/internal/env"
)

func TestCacheRoot(t *testing.T) {
	k := CacheKey()
	xdg, _ := filepath.Abs("xdg") // absolute on every OS
	tests := []struct {
		goos string
		vars map[string]string
		want string
	}{
		{"linux", map[string]string{"XDG_CACHE_HOME": xdg,
			"HOME": "/h"}, filepath.Join(xdg, "gohard", "bundle", k)},
		{"linux", map[string]string{"XDG_CACHE_HOME": "rel",
			"HOME": "/h"}, filepath.Join("/h", ".cache", "gohard", "bundle", k)},
		{"linux", map[string]string{"HOME": "/h"},
			filepath.Join("/h", ".cache", "gohard", "bundle", k)},
		{"darwin", map[string]string{"HOME": "/h"},
			filepath.Join("/h", "Library", "Caches", "gohard", "bundle", k)},
		{"windows", map[string]string{"LocalAppData": `C:\L`, "HOME": "/h"},
			filepath.Join(`C:\L`, "gohard", "bundle", k)},
		{"linux", nil, ""},
		{"windows", map[string]string{"HOME": "/h"}, ""},
	}
	for _, tt := range tests {
		got, err := CacheRoot(env.LookupFunc(tt.vars), tt.goos)
		if tt.want == "" {
			if !errors.Is(err, ErrNoCacheDir) {
				t.Errorf("%s %v: %q, %v; want ErrNoCacheDir", tt.goos,
					tt.vars, got, err)
			}
			continue
		}
		if err != nil || got != tt.want {
			t.Errorf("%s %v: %q, %v; want %q", tt.goos, tt.vars, got, err,
				tt.want)
		}
	}
	if len(k) != 16 {
		t.Fatalf("key %q", k)
	}
}

func TestCachePath(t *testing.T) {
	root := filepath.Join("/c", "b")
	if p, ok := CachePath(root, filepath.Join(root, "ieee",
		"numeric_std.vhdl")); !ok || p != "ieee/numeric_std.vhdl" {
		t.Fatal(p, ok)
	}
	for _, n := range []string{filepath.Join(root, "LICENSE"),
		filepath.Join(root, "ieee", "nope.vhdl"),
		filepath.Join("/c", "ieee", "numeric_std.vhdl")} {
		if _, ok := CachePath(root, n); ok {
			t.Fatal(n)
		}
	}
}

func TestInAnyCache(t *testing.T) {
	file := filepath.Join("ieee", "numeric_std.vhdl")
	for _, base := range []string{filepath.Join("/home", "u", ".cache"),
		filepath.Join("/gohard-cache"), filepath.Join("/x", "gohard",
			"bundle", CacheKey())} {
		n := filepath.Join(base, "gohard", "bundle", CacheKey(), file)
		if p, ok := InAnyCache(n); !ok || p != "ieee/numeric_std.vhdl" {
			t.Errorf("%s: %q %v", n, p, ok)
		}
	}
	for _, n := range []string{
		filepath.Join("/c", "gohard", "bundle", "0123456789abcdef", file),
		filepath.Join("/c", "gohard", "bundle", CacheKey(), "LICENSE"),
		filepath.Join("/c", "gohard", "bundle", CacheKey(), "top.vhd"),
		filepath.Join("/c", "ieee", "numeric_std.vhdl"),
	} {
		if _, ok := InAnyCache(n); ok {
			t.Errorf("%s: cached", n)
		}
	}
}
