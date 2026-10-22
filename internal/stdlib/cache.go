package stdlib

import (
	"errors"
	"fmt"
	"path"
	"path/filepath"
	"strings"

	"github.com/connerohnesorge/gohard/internal/env"
)

// The bundled sources on disk (#33). Editors open files, not embedded
// bytes, so the first location the language server returns into the
// bundle writes the bundled files, read-only, to a cache directory
// named after their content:
//
//	$XDG_CACHE_HOME/gohard/bundle/<key>/ieee/numeric_std.vhdl
//
// A file already there is reused only when it matches its integrity
// hash; any other is rewritten.

// CacheKey names the cache directory of this bundle: the first 16 hex
// digits of the SHA-256 of the integrity manifest, so it changes
// exactly when a bundled file does.
func CacheKey() string { return cacheKey }

var cacheKey = func() string {
	raw, _ := sources.ReadFile(sumsFile)
	return HashHex(raw)[:16]
}()

// ErrNoCacheDir is returned when no cache directory can be found: none
// of the variables CacheRoot reads is set.
var ErrNoCacheDir = errors.New("no cache directory: neither " +
	"XDG_CACHE_HOME nor HOME (LocalAppData on Windows) is set")

// CacheRoot returns the directory the bundled files are written to,
// for the OS goos: $XDG_CACHE_HOME/gohard/bundle/<CacheKey> when
// XDG_CACHE_HOME is set (an absolute path), else
// %LocalAppData%\gohard\bundle\<key> on Windows,
// $HOME/Library/Caches/gohard/bundle/<key> on macOS (the OS cache
// directory) and $HOME/.cache/gohard/bundle/<key> elsewhere. It only
// reads the environment.
func CacheRoot(getenv env.Getenv, goos string) (string, error) {
	base := ""
	if v, ok := getenv("XDG_CACHE_HOME"); ok && env.IsRooted(v) {
		base = v
	} else {
		switch goos {
		case "windows":
			if v, ok := getenv("LocalAppData"); ok && v != "" {
				base = v
			} else if v, ok := getenv("LOCALAPPDATA"); ok && v != "" {
				base = v
			}
		case "darwin", "ios":
			if v, ok := getenv("HOME"); ok && v != "" {
				base = filepath.Join(v, "Library", "Caches")
			}
		default:
			if v, ok := getenv("HOME"); ok && v != "" {
				base = filepath.Join(v, ".cache")
			}
		}
	}
	if base == "" {
		return "", ErrNoCacheDir
	}
	return filepath.Join(base, "gohard", "bundle", CacheKey()), nil
}

// Materialize writes every bundled file (the VHDL sources, LICENSE and
// AUTHORS.md) under root with mode 0444, each through a temp file and a
// rename. A file already there whose bytes match its integrity hash is
// kept; any other is replaced. The bytes written are the embedded ones,
// which must themselves match (Verify); a file that does not is an
// error, not written.
func Materialize(fsys env.FS, root string) error {
	want := map[string]string{}
	for _, s := range Sums() {
		want[s.Path] = s.Hex
	}
	for _, p := range hashed() {
		data, ok := Source(p)
		if !ok || HashHex(data) != want[p] {
			return fmt.Errorf("bundled file %s fails its integrity check", p)
		}
		name := filepath.Join(root, filepath.FromSlash(p))
		if old, err := fsys.ReadFile(name); err == nil &&
			HashHex(old) == want[p] {
			continue
		}
		if err := writeReadOnly(fsys, name, data); err != nil {
			return err
		}
	}
	return nil
}

func writeReadOnly(fsys env.FS, name string, data []byte) error {
	dir := filepath.Dir(name)
	if err := fsys.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := fsys.WriteTemp(dir, "."+filepath.Base(name)+".", data)
	if err != nil {
		return err
	}
	if err := fsys.Chmod(tmp, 0o444); err != nil {
		_ = fsys.Remove(tmp)
		return err
	}
	if _, err := fsys.Lstat(name); err == nil {
		// A stale read-only file: Windows refuses to rename over it.
		_ = fsys.Chmod(name, 0o644)
		_ = fsys.Remove(name)
	}
	if err := fsys.Rename(tmp, name); err != nil {
		_ = fsys.Remove(tmp)
		return err
	}
	return nil
}

// CachePath returns the bundle path (a File.Path) of the OS path name
// when it is a bundled VHDL file under root.
func CachePath(root, name string) (string, bool) {
	rel, err := filepath.Rel(root, name)
	if err != nil {
		return "", false
	}
	p := filepath.ToSlash(rel)
	if !validName(p) || strings.HasPrefix(p, "..") || path.Ext(p) != ".vhdl" {
		return "", false
	}
	_, ok := Lookup(p)
	return p, ok
}
