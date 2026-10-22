package ide

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"maps"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	"github.com/connerohnesorge/gohard/internal/config"
	"github.com/connerohnesorge/gohard/internal/diag"
	"github.com/connerohnesorge/gohard/internal/elab/hier"
	"github.com/connerohnesorge/gohard/internal/env"
	"github.com/connerohnesorge/gohard/internal/libs"
	"github.com/connerohnesorge/gohard/internal/lint"
	"github.com/connerohnesorge/gohard/internal/parser"
	"github.com/connerohnesorge/gohard/internal/sema"
	"github.com/connerohnesorge/gohard/internal/server"
	"github.com/connerohnesorge/gohard/internal/workspace"
)

// Options configure an Analyzer.
type Options struct {
	// ToolVersion is the gohard version seen by conditional analysis
	// (TOOL_VERSION), as for `gohard check`.
	ToolVersion string
	// Jobs runs the pure per-file and per-unit jobs of sema (nil:
	// env.Sequential) with at most Limit at once (-p N). They change
	// only how fast a slice finishes, never a result.
	Jobs  env.Jobs
	Limit int
	// Probe, when set, observes each analysis (the #19 latency
	// benchmark): sema's phases, plus "parse" as an analysis starts,
	// "hierarchy" before the elaboration checks and "analyzed" once
	// the diagnostics are grouped. It never changes a result.
	Probe *sema.Probe
	// Libraries is the host the "library not found" quick fix
	// searches for vendor libraries like `gohard libs detect` (#1); a
	// nil FS turns that search off.
	Libraries libs.Host
	// Cache, when set, opens the on-disk analysis cache (#37) of a
	// project root for a configuration, or returns nil when there is
	// none. Each analysis restores what it can from it and stores the
	// rest; it never changes a result.
	Cache func(root string, cfg config.Config) sema.Cache
	// Files is the project as the editor's simulation runs read it
	// (#18), the same view gohard check --sim gives a design: files
	// below the snapshot's root, never .gohard. A run's writes are
	// dropped. Nil means the runs find no project file (a session
	// replay, whose recorded root is not on this disk).
	Files env.FS
}

// phase reports the start of an analysis phase to the probe.
func (a *Analyzer) phase(name string) {
	if pr := a.opts.Probe; pr != nil && pr.Phase != nil {
		pr.Phase(name)
	}
}

// Analyzer is the server.Analyzer of `gohard lsp`. Methods it does not
// define come from server.Nop; Features reports exactly the features
// the package implements.
type Analyzer struct {
	server.Nop
	opts Options

	mu sync.Mutex
	// parses are parse trees by parse key (configuration and text),
	// from the latest two analyses.
	parses, prevParses map[string]*parser.Result
	// results are the latest analyses by content key, newest last.
	results []*Analysis
	// cache is the analysis cache opened last, for cacheKey (the root
	// and the configuration).
	cache    sema.Cache
	cacheKey string
}

var _ server.Analyzer = (*Analyzer)(nil)

// New returns an analyzer.
func New(o Options) *Analyzer { return &Analyzer{opts: o} }

// provided are the features this package implements; feature files
// add theirs with provide in an init function.
var provided = server.FeatureDiagnostics

func provide(f server.Feature) { provided |= f }

// Features implements server.Analyzer.
func (a *Analyzer) Features() server.Features {
	f := server.Features{Set: provided}
	if f.Has(server.FeatureCompletion) {
		f.CompletionTriggers = []string{"."}
	}
	return f
}

// Diagnostics implements server.Analyzer: the syntax and semantic
// diagnostics of every project file, which once the server is idle
// equal `gohard check` on the same text (#25).
func (a *Analyzer) Diagnostics(snap *server.Snapshot,
) server.Task[[]server.FileDiagnostics] {
	return Query(a, snap, func(an *Analysis) []server.FileDiagnostics {
		return an.Diagnostics
	})
}

// Query returns a task that analyzes snap (or reuses its analysis)
// and then answers f from it, in the slice the analysis finishes. f
// must be a pure function of the analysis.
func Query[T any](a *Analyzer, snap *server.Snapshot,
	f func(*Analysis) T,
) server.Task[T] {
	return then(a.Analysis(snap), f)
}

func then[T any](t server.Task[*Analysis], f func(*Analysis) T,
) server.Task[T] {
	return server.TaskFunc[T](func(budget int) server.Slice[T] {
		sl := t.Run(budget)
		if sl.Done || sl.Next == nil {
			return server.Slice[T]{Done: true, Value: f(sl.Value),
				Report: sl.Report}
		}
		return server.Slice[T]{Next: then(sl.Next, f), Report: sl.Report}
	})
}

// BytesPerStep is the parse work of one budget step: a slice with
// budget B parses files of about B*BytesPerStep bytes (at least one
// file).
const BytesPerStep = 32

// Analysis returns the task that analyzes snap. It takes 2+P slices:
// planning, P parse slices of about budget*BytesPerStep bytes each,
// and the analysis. P depends only on the snapshot and the budget.
func (a *Analyzer) Analysis(snap *server.Snapshot,
) server.Task[*Analysis] {
	return server.TaskFunc[*Analysis](func(budget int,
	) server.Slice[*Analysis] {
		a.phase("parse")
		p := a.plan(snap, budget)
		return server.Slice[*Analysis]{Next: p.parse(0),
			Report: p.report(0)}
	})
}

// source is one file of a plan.
type source struct {
	doc  *server.Document
	path string // the sema path: Document.Rel
	key  string // parse key
}

type plan struct {
	a      *Analyzer
	snap   *server.Snapshot
	cfg    config.Config
	key    string // content key of the whole analysis
	srcs   []source
	chunks []int // srcs[chunks[i]:chunks[i+1]] is parse slice i
	trees  []*parser.Result
}

// Files returns the documents of snap that are analyzed, by sema
// path: VHDL documents with a path (the project files on disk and
// open VHDL buffers, also outside the root), sorted by path.
func Files(snap *server.Snapshot) []*server.Document {
	var out []*server.Document
	for _, d := range snap.Documents() {
		if d.Rel == "" {
			continue
		}
		if !d.Open || workspace.IsVHDL(d.Path) || d.LanguageID == "vhdl" {
			out = append(out, d)
		}
	}
	slices.SortStableFunc(out, func(x, y *server.Document) int {
		return strings.Compare(x.Rel, y.Rel)
	})
	return slices.CompactFunc(out, func(x, y *server.Document) bool {
		return x.Rel == y.Rel
	})
}

// Config returns the configuration sema sees for snap: the snapshot's
// configuration with every library folder written as a root-relative
// slash path, the form of the file paths, as `gohard check` does.
func Config(snap *server.Snapshot) config.Config {
	cfg := snap.Config
	libs := slices.Clone(cfg.Libraries)
	for i, lib := range libs {
		lib.Folders = slices.Clone(lib.Folders)
		for j, f := range lib.Folders {
			lib.Folders[j].Path = rootRelative(snap.Root, f.Path)
		}
		libs[i] = lib
	}
	cfg.Libraries = libs
	return cfg
}

func rootRelative(root, folder string) string {
	abs := env.Abs(root, filepath.FromSlash(strings.ReplaceAll(folder,
		`\`, "/")))
	r, err := filepath.Rel(root, abs)
	if err != nil {
		return filepath.ToSlash(abs)
	}
	return filepath.ToSlash(r)
}

func hash(parts ...[]byte) string {
	h := sha256.New()
	for _, p := range parts {
		var n [8]byte
		for i := range n {
			n[i] = byte(len(p) >> (8 * i))
		}
		h.Write(n[:])
		h.Write(p)
	}
	return hex.EncodeToString(h.Sum(nil))
}

func (a *Analyzer) plan(snap *server.Snapshot, budget int) *plan {
	p := &plan{a: a, snap: snap, cfg: Config(snap)}
	cj := p.cfg.Identity()
	ck := hash(cj, []byte(a.opts.ToolVersion))
	keys := [][]byte{[]byte(ck), []byte(snap.Root)}
	for _, d := range Files(snap) {
		k := hash([]byte(ck), d.Text())
		p.srcs = append(p.srcs, source{doc: d, path: d.Rel, key: k})
		keys = append(keys, []byte(d.URI), []byte(d.Rel), []byte(k))
	}
	for _, dg := range snap.Discovery {
		b, _ := json.Marshal(dg)
		keys = append(keys, b)
	}
	p.key = hash(keys...)
	limit := max(budget, 1) * BytesPerStep
	p.chunks = []int{0}
	n := 0
	for i, s := range p.srcs {
		n += len(s.doc.Text())
		if n >= limit || i == len(p.srcs)-1 {
			p.chunks = append(p.chunks, i+1)
			n = 0
		}
	}
	p.trees = make([]*parser.Result, len(p.srcs))
	return p
}

func (p *plan) report(i int) *server.Report {
	n := len(p.chunks) - 1
	if n == 0 {
		return nil
	}
	return &server.Report{Message: "parsing",
		Percentage: 100 * i / (n + 1)}
}

// parse returns the task of parse slice i, then the analysis.
func (p *plan) parse(i int) server.Task[*Analysis] {
	return server.TaskFunc[*Analysis](func(int) server.Slice[*Analysis] {
		if i >= len(p.chunks)-1 {
			return server.Slice[*Analysis]{Done: true, Value: p.analyze()}
		}
		if p.a.cached(p.key) == nil {
			popts := parser.FromConfig(p.cfg, p.a.opts.ToolVersion)
			c := p.a.diskCache(p)
			for j := p.chunks[i]; j < p.chunks[i+1]; j++ {
				s := p.srcs[j]
				t := p.a.tree(s.key)
				switch {
				case t != nil:
				case c != nil:
					t = sema.ParseCached(c, s.path, s.doc.Text(), popts)
				default:
					t = parser.Parse(s.doc.Text(), popts)
				}
				p.trees[j] = t
			}
		}
		return server.Slice[*Analysis]{Next: p.parse(i + 1),
			Report: p.report(i + 1)}
	})
}

func (a *Analyzer) cached(key string) *Analysis {
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, r := range a.results {
		if r.key == key {
			return r
		}
	}
	return nil
}

func (a *Analyzer) tree(key string) *parser.Result {
	a.mu.Lock()
	defer a.mu.Unlock()
	if t, ok := a.parses[key]; ok {
		return t
	}
	return a.prevParses[key]
}

// store keeps an analysis and its trees: the trees of the latest two
// analyses and the latest two results.
func (a *Analyzer) store(an *Analysis, p *plan) {
	a.mu.Lock()
	defer a.mu.Unlock()
	trees := make(map[string]*parser.Result, len(p.srcs))
	for i, s := range p.srcs {
		if p.trees[i] != nil {
			trees[s.key] = p.trees[i]
		}
	}
	a.prevParses, a.parses = a.parses, trees
	a.results = append(a.results, an)
	if len(a.results) > 2 {
		a.results = slices.Delete(a.results, 0, len(a.results)-2)
	}
}

// diskCache returns the analysis cache of the plan's root and
// configuration, opening it when the last one was for another.
func (a *Analyzer) diskCache(p *plan) sema.Cache {
	if a.opts.Cache == nil || p.snap.Root == "" {
		return nil
	}
	cj := p.cfg.Identity()
	key := hash([]byte(p.snap.Root), cj)
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.cacheKey != key {
		a.cache, a.cacheKey = a.opts.Cache(p.snap.Root, p.cfg), key
	}
	return a.cache
}

// analyze runs the analysis of the plan (or reuses a stored one).
func (p *plan) analyze() *Analysis {
	if r := p.a.cached(p.key); r != nil {
		return r.at(p.snap)
	}
	an := &Analysis{Snap: p.snap, key: p.key,
		uriOf: map[string]string{}, libs: p.a.opts.Libraries}
	for _, s := range p.srcs {
		an.uriOf[s.path] = s.doc.URI
	}
	var ds []diag.Diagnostic
	ds = append(ds, p.snap.Discovery...)
	if len(p.srcs) > 0 {
		in := sema.Input{Config: p.cfg, ToolVersion: p.a.opts.ToolVersion,
			Jobs: p.a.opts.Jobs, Limit: p.a.opts.Limit,
			Probe: p.a.opts.Probe, Cache: p.a.diskCache(p)}
		for i, s := range p.srcs {
			t := p.trees[i]
			if t == nil { // the trees came from a stored analysis
				t = p.a.tree(s.key)
			}
			in.Files = append(in.Files, sema.Source{Path: s.path,
				Text: s.doc.Text(), Parse: t})
		}
		an.Result = sema.Analyze(in)
		ds = append(ds, an.Result.Diagnostics...)
		p.a.phase("hierarchy")
		ds = append(ds, hier.Build(an.Result, hier.Options{}).Diagnostics...)
		ds = append(ds, lint.Check(an.Result, p.cfg.Lint)...)
	}
	an.Diagnostics = group(an, diag.Normalize(ds))
	p.a.phase("analyzed")
	for i, s := range p.srcs {
		if f, ok := an.Result.File(s.path); ok && p.trees[i] == nil {
			p.trees[i] = f.Parse
		}
	}
	p.a.store(an, p)
	return an
}

// group splits normalized diagnostics by document, sorted by URI,
// leaving out those without a path or of a file that is not a
// document of the snapshot.
func group(an *Analysis, ds []diag.Diagnostic,
) []server.FileDiagnostics {
	by := map[string][]diag.Diagnostic{}
	for _, d := range ds {
		if uri, ok := an.uriOf[d.Path]; ok && d.Path != "" {
			by[uri] = append(by[uri], d)
		}
	}
	out := make([]server.FileDiagnostics, 0, len(by))
	for _, uri := range slices.Sorted(maps.Keys(by)) {
		out = append(out, server.FileDiagnostics{URI: uri,
			Diagnostics: by[uri]})
	}
	return out
}
