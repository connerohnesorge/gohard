package cli

import (
	"context"
	"runtime/debug"
	"strings"

	"github.com/connerohnesorge/gohard/v2/internal/repro"
	"github.com/connerohnesorge/gohard/v2/internal/stdlib"
)

// Commit is the commit gohard was built from. Release builds set it
// with -ldflags "-X github.com/connerohnesorge/gohard/v2/internal/cli.Commit=…"
// (goreleaser, Nix); otherwise SetBuildInfo takes vcs.revision from the
// Go build info.
var Commit = ""

// goVersion is the Go toolchain that built gohard, set by SetBuildInfo.
var goVersion = ""

// Build is what a gohard binary knows about itself (#39): what `gohard
// version` prints and the repro and session headers record. Unknown
// facts are repro.Unknown.
type Build struct {
	Version string
	Commit  string // "-dirty" suffix when built from a modified tree
	IEEE    string // stdlib.IEEE: "<tag> <commit>"
	Go      string
}

// SetBuildInfo completes Version and Commit from bi where -ldflags left
// them unset, and records bi's Go toolchain. cmd/gohard calls it once
// with debug.ReadBuildInfo before Run; bi may be nil. Tests never call
// it, so their output shows the commit and toolchain as unknown.
func SetBuildInfo(bi *debug.BuildInfo) {
	b := resolveBuild(Version, Commit, bi)
	Version, Commit, goVersion = b.Version, b.Commit, b.Go
}

// resolveBuild combines the -ldflags values with the Go build info:
// a version other than devVersion and a non-empty commit come from
// -ldflags (goreleaser, Nix) and win. Otherwise the version is the
// module version without its "v" (`go install ...@v1.2.3`, or the
// pseudo-version `go build` stamps in a checkout), and the commit is
// vcs.revision with "-dirty" when vcs.modified is true (`go build` in
// a checkout), else the revision of a pseudo-version.
func resolveBuild(version, commit string, bi *debug.BuildInfo) Build {
	b := Build{Version: version, Commit: commit, IEEE: stdlib.IEEE()}
	if bi != nil {
		mv := bi.Main.Version
		if b.Version == devVersion && mv != "" && mv != "(devel)" {
			b.Version = strings.TrimPrefix(mv, "v")
		}
		if b.Commit == "" {
			b.Commit = vcsCommit(bi.Settings)
		}
		if b.Commit == "" {
			b.Commit = pseudoRevision(mv)
		}
		b.Go = bi.GoVersion
	}
	for _, f := range []*string{&b.Version, &b.Commit, &b.Go} {
		if *f == "" {
			*f = repro.Unknown
		}
	}
	return b
}

func vcsCommit(settings []debug.BuildSetting) string {
	var rev, modified string
	for _, s := range settings {
		switch s.Key {
		case "vcs.revision":
			rev = s.Value
		case "vcs.modified":
			modified = s.Value
		}
	}
	if rev != "" && modified == "true" {
		rev += "-dirty"
	}
	return rev
}

// pseudoRevision returns the 12-digit revision of a pseudo-version
// such as v0.0.0-20261020114936-a57bb0b24672 (or with "+dirty"), or "".
func pseudoRevision(v string) string {
	v, dirty := strings.CutSuffix(v, "+dirty")
	i := strings.LastIndexByte(v, '-')
	if i < 0 {
		return ""
	}
	rev, ts := v[i+1:], v[:i]
	j := strings.LastIndexAny(ts, "-.")
	if len(rev) != 12 || strings.Trim(rev, "0123456789abcdef") != "" ||
		j < 0 || len(ts[j+1:]) != 14 ||
		strings.Trim(ts[j+1:], "0123456789") != "" {
		return ""
	}
	if dirty {
		rev += "-dirty"
	}
	return rev
}

type buildKey struct{}

// WithBuildInfo returns a context under which `gohard version` and the
// repro and session headers describe a binary built with the -ldflags
// values version and commit ("" when unset) and the Go build info bi.
// It is the test hook of the testdata/cli `build-info` setup directive
// (#39): only tests call it, and a context is the only way in.
func WithBuildInfo(ctx context.Context, version, commit string,
	bi *debug.BuildInfo,
) context.Context {
	return context.WithValue(ctx, buildKey{},
		resolveBuild(version, commit, bi))
}

// build is the Build of this run: the WithBuildInfo value when the
// context has one, else the binary's own.
func (inv *Invocation) build() Build {
	if inv.Ctx != nil {
		if b, ok := inv.Ctx.Value(buildKey{}).(Build); ok {
			return b
		}
	}
	b := Build{Version: Version, Commit: Commit, IEEE: stdlib.IEEE(),
		Go: goVersion}
	for _, f := range []*string{&b.Commit, &b.Go} {
		if *f == "" {
			*f = repro.Unknown
		}
	}
	return b
}

// runVersion is `gohard version`: one "<key> <value>" line each for
// the version, the commit, the bundled IEEE library and the Go
// toolchain, in that order; an unknown value is "unknown".
func runVersion(inv *Invocation) Result {
	if len(inv.Args) > 0 {
		return inv.usage("gohard version takes no arguments")
	}
	b := inv.build()
	return Result{Stdout: []byte("version " + b.Version + "\n" +
		"commit " + b.Commit + "\n" +
		"ieee " + b.IEEE + "\n" +
		"go " + b.Go + "\n")}
}
