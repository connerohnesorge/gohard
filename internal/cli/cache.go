package cli

import (
	"strings"
	"sync"

	"github.com/connerohnesorge/gohard/v2/internal/cache"
	"github.com/connerohnesorge/gohard/v2/internal/config"
	"github.com/connerohnesorge/gohard/v2/internal/env"
	"github.com/connerohnesorge/gohard/v2/internal/sema"
)

// binaryID identifies the gohard executable itself (a hash of its
// content), set by cmd/gohard with SetBinary: with it a rebuilt
// development binary of the same version and commit never reads the
// analysis cache an older build wrote (#37), even when the two have the
// same size and time (nix store and snapshot builds). It is computed
// once, when a command first opens the cache. Tests leave it unset.
var binaryID = func() string { return "" }

// noBinary is the identity of an executable that could not be read:
// the code is unknown, so the cache stays off.
const noBinary = "unreadable"

// SetBinary records how to identify the running executable for the
// analysis cache key: id is called at most once, the first time a
// command opens the cache, and returns a hash of the executable (or
// "unreadable", which turns the cache off). cmd/gohard calls it once
// before Run.
func SetBinary(id func() string) { binaryID = sync.OnceValue(id) }

// cacheVersion is the GoHard build part of the whole-cache key: the
// version, the commit (with -dirty for a modified tree) and the
// executable's identity.
func cacheVersion(b Build) string {
	return b.Version + "\x00" + b.Commit + "\x00" + binaryID()
}

// openCache opens the on-disk analysis cache of the project root for
// an analysis of cfg (#37): <root>/.gohard/cache. It returns nil when
// the folder cannot be written, so the run goes on without the cache;
// the cache never prints anything.
func (inv *Invocation) openCache(root string, cfg config.Config,
) sema.Cache {
	return openCache(inv.Env.FS, cacheVersion(inv.build()), root, cfg)
}

func openCache(fsys env.FS, version, root string, cfg config.Config,
) sema.Cache {
	if root == "" || strings.HasSuffix(version, "\x00"+noBinary) {
		return nil
	}
	st := cache.Open(fsys, cache.Dir(root),
		cache.NewKey(version, sema.CacheSchema(), cfg))
	if !st.Enabled() {
		return nil
	}
	return st
}

// cacheOpener is the language server's way to the cache
// (ide.Options.Cache): it opens the cache of a snapshot's root for
// the snapshot's configuration.
func (inv *Invocation) cacheOpener() func(root string,
	cfg config.Config) sema.Cache {
	return func(root string, cfg config.Config) sema.Cache {
		return inv.openCache(root, cfg)
	}
}

// CacheOpener is the ide.Options.Cache of `gohard lsp` for a program
// that builds the language server itself (the #19 benchmark): the
// analysis cache of a root through fsys, keyed by this binary's build.
func CacheOpener(fsys env.FS) func(root string,
	cfg config.Config) sema.Cache {
	inv := &Invocation{}
	v := cacheVersion(inv.build())
	return func(root string, cfg config.Config) sema.Cache {
		return openCache(fsys, v, root, cfg)
	}
}
