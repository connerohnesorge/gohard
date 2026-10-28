package server

import (
	"bytes"
	"encoding/json"
	"errors"
	"maps"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"

	"github.com/connerohnesorge/gohard/v2/internal/env"
	"github.com/connerohnesorge/gohard/v2/internal/jsonrpc2"
	"github.com/connerohnesorge/gohard/v2/internal/stdlib"
)

// BundleScheme is the URI scheme the core gives the bundled std/ieee
// sources (#33): gohard-bundle:///ieee/numeric_std.vhdl. It never
// reaches the client: the Driver writes the bundled files to the cache
// directory (stdlib.CacheRoot) the first time a message names one and
// sends their file:// URIs instead, and it maps a file URI of that
// directory back to the bundle URI in every client message, so an
// editor that opens a cached file gets the bundled unit, never a file
// of the work library.
const BundleScheme = "gohard-bundle"

const bundlePrefix = BundleScheme + ":///"

// BundleURI returns the core URI of the bundled file at bundle path p.
func BundleURI(p string) string { return bundlePrefix + p }

// BundlePath returns the bundle path of a core bundle URI.
func BundlePath(uri string) (string, bool) {
	p, ok := strings.CutPrefix(uri, bundlePrefix)
	return p, ok && p != ""
}

var bundleRef = regexp.MustCompile(`gohard-bundle:///[A-Za-z0-9_./-]+`)

// bundleFiles is the Driver's view of the bundle cache. The root is
// found from the environment the first time a message may need it and
// the files are written the first time an outgoing message names one,
// so startup does no work (TestStartupNoAnalysis).
type bundleFiles struct {
	looked   bool
	root     string
	rootErr  error
	written  bool
	err      error
	reported bool
}

func (b *bundleFiles) lookup(e env.Env) (string, error) {
	if !b.looked {
		b.looked = true
		getenv := e.Getenv
		if getenv == nil {
			getenv = func(string) (string, bool) { return "", false }
		}
		b.root, b.rootErr = stdlib.CacheRoot(getenv, runtime.GOOS)
	}
	return b.root, b.rootErr
}

// ensure writes the bundled files once and returns their root.
func (b *bundleFiles) ensure(e env.Env) (string, error) {
	root, err := b.lookup(e)
	if err != nil {
		return "", err
	}
	if !b.written {
		b.written = true
		if e.FS == nil {
			b.err = errNoFS
		} else {
			b.err = stdlib.Materialize(e.FS, root)
		}
	}
	return root, b.err
}

var errNoFS = errors.New("no file system")

// incoming maps every file URI of a cached bundled file in a client
// message to its bundle URI. Other messages are returned unchanged.
func (b *bundleFiles) incoming(e env.Env, body []byte) []byte {
	if !bytes.Contains(body, []byte(stdlib.CacheKey())) {
		return body
	}
	root, err := b.lookup(e)
	if err != nil {
		return body
	}
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.UseNumber()
	var v any
	if dec.Decode(&v) != nil {
		return body
	}
	changed := false
	var walk func(any) any
	walk = func(x any) any {
		switch x := x.(type) {
		case string:
			if p, ok := URIToPath(x); ok {
				if bp, ok := stdlib.CachePath(root, p); ok {
					changed = true
					return BundleURI(bp)
				}
			}
		case []any:
			for i := range x {
				x[i] = walk(x[i])
			}
		case map[string]any:
			for _, k := range slices.Sorted(maps.Keys(x)) {
				x[k] = walk(x[k])
			}
		}
		return x
	}
	v = walk(v)
	if !changed {
		return body
	}
	return marshal(v)
}

// outgoing replaces every bundle URI in m with the file URI of the
// cached file. When the files cannot be written the bundled entries of
// a result array are left out, and the first such message is preceded
// by an error the client shows (sent through send).
func (b *bundleFiles) outgoing(e env.Env, m jsonrpc2.Message,
	send func(Send) error,
) (jsonrpc2.Message, error) {
	var raw *json.RawMessage
	switch x := m.(type) {
	case *jsonrpc2.Response:
		c := *x
		m, raw = &c, &c.Result
	case *jsonrpc2.Notification:
		c := *x
		m, raw = &c, &c.Params
	case *jsonrpc2.Request:
		c := *x
		m, raw = &c, &c.Params
	default:
		return m, nil
	}
	if !bytes.Contains(*raw, []byte(bundlePrefix)) {
		return m, nil
	}
	root, err := b.ensure(e)
	if err == nil {
		*raw = bundleRef.ReplaceAllFunc(*raw, func(u []byte) []byte {
			p := strings.TrimPrefix(string(u), bundlePrefix)
			return []byte(PathToURI(filepath.Join(root,
				filepath.FromSlash(p))))
		})
		return m, nil
	}
	if !b.reported {
		b.reported = true
		where := ""
		if root != "" {
			where = " to " + root
		}
		msg := "gohard: cannot write the bundled std/ieee sources" +
			where + ": " + err.Error() + "; locations in them are " +
			"left out"
		if serr := send(Send{&jsonrpc2.Notification{
			Method: "window/showMessage",
			Params: marshal(struct {
				Type    int    `json:"type"`
				Message string `json:"message"`
			}{LogError, msg}),
		}}); serr != nil {
			return m, serr
		}
	}
	var items []json.RawMessage
	if json.Unmarshal(*raw, &items) == nil {
		kept := []json.RawMessage{}
		for _, it := range items {
			if !bytes.Contains(it, []byte(bundlePrefix)) {
				kept = append(kept, it)
			}
		}
		*raw = marshal(kept)
	}
	return m, nil
}

// dropCached leaves the cached bundled files out of a workspace read:
// a session whose folder is the cache (an editor that roots a file it
// opened at its own directory) must not analyze them into work.
func (b *bundleFiles) dropCached(e env.Env, w WorkspaceLoaded,
) WorkspaceLoaded {
	root, err := b.lookup(e)
	if err != nil {
		return w
	}
	kept := w.Sources[:0:0]
	for _, s := range w.Sources {
		if _, ok := stdlib.CachePath(root, env.Abs(w.Root, s.Name)); !ok {
			kept = append(kept, s)
		}
	}
	if len(kept) == len(w.Sources) {
		return w
	}
	w.Sources = kept
	return w
}
