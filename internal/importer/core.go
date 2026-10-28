package importer

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"maps"
	"path"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"go.yaml.in/yaml/v3"

	"github.com/connerohnesorge/gohard/internal/diag"
)

// coreFormat names the format in import/syntax messages.
const coreFormat = "FuseSoC .core file"

// coreStandards maps the FuseSoC VHDL file types to gohard standards.
// GoHard's oldest standard is VHDL-1993, so vhdlSource-87 reads as
// 1993; plain vhdlSource names none.
var coreStandards = map[string]string{
	"vhdlSource": "", "vhdlSource-87": "1993", "vhdlSource-93": "1993",
	"vhdlSource-2008": "2008", "vhdlSource-2019": "2019",
}

// coreFlagRE matches a FuseSoC use-flag condition: "flag? (item)" or
// "!flag? (item)".
var coreFlagRE = regexp.MustCompile(`^!?[A-Za-z0-9_-]+\?\s*\(`)

// coreOps are the version operators of a depend: entry, longest first.
var coreOps = []string{">=", "<=", ">", "<", "=", "^", "~"}

// coreBudget is the most steps one import of a .core file takes, with
// the .core files it reads to resolve depend: entries, and
// coreByteBudget the most .core text it parses (P7). A step is one
// YAML key or list item read, or one candidate core compared with a
// depend: entry, plus one per coreTextStep bytes of the text it reads
// or compares. An item reached through a YAML alias counts each time
// it is used, so a small file whose aliases fan out to millions of
// files stops at the budget. Reading all 17 .core files of the pinned
// open-logic takes 569 steps and 22 kilobytes (TestCoreBudgetMargin).
// A 4 MiB list of one-letter items, the most YAML the byte budget
// lets through, parses into about 420 MB of YAML nodes.
const (
	coreBudget     = 100000
	coreByteBudget = 4 << 20
)

// coreTextStep is the text one step pays for.
const coreTextStep = 64

// coreMark is how many characters apart off keeps the byte offsets of
// a line.
const coreMark = 64

// ParseCore reads a FuseSoC CAPI2 .core file (#64): every file of
// every fileset whose file_type (the file's own, else the fileset's)
// is vhdlSource, vhdlSource-87 or -93 (both read as 1993), -2008 or
// -2019, in the library of its logical_name (work when absent). Paths
// are relative to the folder of the .core file. Each depend: entry
// ([op]vendor:library:name[:version], op one of = >= > <= < ^ ~, no
// op with a version meaning =) resolves among this core and
// files.Cores() to one core per vendor:library:name, picked by solve
// so that every depend: entry matches its pick, as FuseSoC's solver
// does. The files of a core come after the files of the cores it
// depends on. A dependency nothing matches is
// import/unresolved-dependency; when no picks satisfy every
// dependency, the first dependency on a name picks its newest match
// and a later one that does not match it is
// import/dependency-conflict. Files of another type, provider:
// (nothing is fetched) and entries with a use-flag condition are
// skipped with an import/ignored hint; targets, generators and every
// other key are skipped silently. The import takes at most coreBudget
// steps and parses at most coreByteBudget bytes of .core files; past
// either it stops with import/syntax.
func ParseCore(name string, src []byte, files Files) Project {
	r := &coreRun{files: files, main: name, visited: map[string]bool{},
		chosen: map[vlnv]*coreFile{}, byName: map[vlnv][]*coreFile{},
		sorted: map[vlnv]bool{}}
	c := readCore(path.Base(name), name, src, &r.b)
	switch {
	case r.b.over:
		r.p.Diagnostics = append(c.diags, r.b.stop)
		return r.p
	case c.fatal:
		r.p.Diagnostics = c.diags
		return r.p
	}
	if c.named {
		// A dependency may lead back to the project file's own core.
		r.add(c)
		r.chosen[c.vlnv.unversioned()] = c
	}
	r.solve(c)
	r.visit(c)
	if r.b.over {
		r.p.Diagnostics = append(r.p.Diagnostics, r.b.stop)
	}
	return r.p
}

// coreRun is one import of a .core file and the cores it depends on.
type coreRun struct {
	files Files
	main  string // the project file, as diagnostics show it
	p     Project
	b     coreSpent
	// byName holds the cores a depend: may resolve to, by
	// vendor:library:name (no version), newest first once sorted; among
	// equal versions the project file's own core, then files.Cores()
	// order.
	byName  map[vlnv][]*coreFile
	loaded  bool          // files.Cores() is read
	sorted  map[vlnv]bool // byName[key] is sorted
	visited map[string]bool
	// chosen is the core each vendor:library:name resolves to: solve's
	// picks, or else the core the first dependency on it resolved to.
	chosen map[vlnv]*coreFile
}

// coreNeed is a depend: entry d of the core from.
type coreNeed struct {
	from *coreFile
	d    coreDep
}

// coreSpent is what one import has spent of its budgets.
type coreSpent struct {
	steps, bytes int
	over         bool            // a budget ran out: the import stops
	stop         diag.Diagnostic // the import/syntax error that says so
}

// coreFile is one .core file read: its own entries, hints and
// dependencies.
type coreFile struct {
	rel     string // relative to the project file's folder
	l       lines
	src     []byte
	vlnv    vlnv
	named   bool // it has a valid name:
	entries []Entry
	diags   []diag.Diagnostic
	deps    []coreDep
	fatal   bool // not a CAPI2 file
	b       *coreSpent
	// marks holds, for each line off has converted, the byte offset
	// of every coreMark-th character.
	marks map[int][]int
}

// coreDep is one depend: entry.
type coreDep struct {
	op   string
	want vlnv
	text string
	off  int
}

// vlnv is a FuseSoC core name, vendor:library:name:version.
type vlnv struct{ vendor, library, name, version string }

// String returns v as a core name, vendor:library:name:version.
func (v vlnv) String() string {
	s := v.vendor + ":" + v.library + ":" + v.name
	if v.version != "" {
		s += ":" + v.version
	}
	return s
}

// unversioned returns v without its version.
func (v vlnv) unversioned() vlnv {
	v.version = ""
	return v
}

var utf8BOM = []byte("\ufeff")

// add makes the core c one a depend: may resolve to.
func (r *coreRun) add(c *coreFile) {
	key := c.vlnv.unversioned()
	r.byName[key] = append(r.byName[key], c)
}

// visit adds c's dependencies, then c's own entries; it stops when the
// budget runs out.
func (r *coreRun) visit(c *coreFile) {
	r.visited[c.rel] = true
	r.p.Diagnostics = append(r.p.Diagnostics, c.diags...)
	for _, d := range c.deps {
		dep, conflict := r.resolve(coreNeed{c, d})
		if r.b.over {
			return
		}
		switch {
		case conflict != nil:
			r.p.Diagnostics = append(r.p.Diagnostics, c.l.at(diag.New(
				diag.ImportDependencyConflict, d.text,
				conflict.vlnv.String()), d.off))
			continue
		case dep == nil:
			r.p.Diagnostics = append(r.p.Diagnostics, c.l.at(diag.New(
				diag.ImportUnresolvedDependency, d.text), d.off))
			continue
		}
		if !r.visited[dep.rel] {
			r.visit(dep)
		}
	}
	r.p.Entries = append(r.p.Entries, c.entries...)
}

// resolve returns the core the dependency n resolves to: the core
// chosen for its vendor:library:name when n matches it, else (no core,
// conflict) that core, unless no core matches n at all; with none
// chosen yet, the newest core n matches, which becomes the choice. It
// returns nils when no core matches or the budget runs out.
func (r *coreRun) resolve(n coreNeed) (core, conflict *coreFile) {
	key := n.d.want.unversioned()
	if c, ok := r.chosen[key]; ok {
		match, more := r.test(n, c)
		switch {
		case !more:
			return nil, nil
		case match:
			return c, nil
		}
		if r.newest(n) != nil {
			return nil, c
		}
		return nil, nil
	}
	c := r.newest(n)
	if c != nil {
		r.chosen[key] = c
	}
	return c, nil
}

// newest returns the newest core n matches, or nil when none does or
// the budget runs out.
func (r *coreRun) newest(n coreNeed) *coreFile {
	for _, c := range r.cores(n) {
		match, more := r.test(n, c)
		if !more {
			return nil
		}
		if match {
			return c
		}
	}
	return nil
}

// solve picks the core each vendor:library:name that root depends on,
// directly or through the cores picked, resolves to, so that every
// depend: entry of root and of the cores picked matches its pick, as
// FuseSoC's dependency solver requires. It takes the depend: entries
// breadth first from root, each core's in file order; for a name not
// yet picked it tries the cores the entry matches newest first, and
// when a later entry does not match a pick it goes back to the last
// pick with a core left to try (backtracking). A depend: entry that no
// core matches constrains nothing (visit reports it). The first picks
// found go to r.chosen; when there are none, or the budget runs out,
// r.chosen keeps root alone. Every core compared costs steps, so the
// search ends within coreBudget.
func (r *coreRun) solve(root *coreFile) {
	var queue []coreNeed
	push := func(c *coreFile) {
		for _, d := range c.deps {
			queue = append(queue, coreNeed{c, d})
		}
	}
	push(root)
	sel := maps.Clone(r.chosen)
	var try func(i int) bool
	try = func(i int) bool {
		for ; i < len(queue); i++ {
			n := queue[i]
			key := n.d.want.unversioned()
			if c, ok := sel[key]; ok {
				match, more := r.test(n, c)
				switch {
				case !more:
					return false
				case !match && r.newest(n) != nil:
					return false // a conflict
				}
				continue
			}
			tried := false
			for _, c := range r.cores(n) {
				match, more := r.test(n, c)
				if !more {
					return false
				}
				if !match {
					continue
				}
				tried = true
				sel[key] = c
				k := len(queue)
				push(c)
				if try(i + 1) {
					return true
				}
				queue = queue[:k]
			}
			delete(sel, key)
			if tried || r.b.over {
				return false
			}
		}
		return true
	}
	if try(0) {
		r.chosen = sel
	}
}

// cores returns the cores of n's vendor:library:name, newest first;
// the first call reads files.Cores(). It returns nil when the budget
// runs out.
func (r *coreRun) cores(n coreNeed) []*coreFile {
	if !r.loaded {
		r.loaded = true
		for _, p := range r.files.Cores() {
			src, err := r.files.Read(p)
			if err != nil {
				continue // an unreadable core provides nothing
			}
			c := readCore(p, join(path.Dir(r.main), p), src, &r.b)
			if r.b.over {
				return nil
			}
			if !c.fatal && c.named {
				r.add(c)
			}
		}
	}
	key := n.d.want.unversioned()
	cs := r.byName[key]
	if !r.sorted[key] {
		r.sorted[key] = true
		// Sorting compares versions, which costs steps as test does.
		slices.SortStableFunc(cs, func(a, b *coreFile) int {
			n.from.charge(1+(len(a.vlnv.version)+len(b.vlnv.version))/
				coreTextStep, n.at)
			return compareVersions(b.vlnv.version, a.vlnv.version)
		})
	}
	if r.b.over {
		return nil
	}
	return cs
}

// test reports whether the core c satisfies the dependency n, and
// whether the budget lets the import go on: comparing costs a step,
// plus one per coreTextStep bytes of the name and versions compared,
// so long versions do not multiply the work.
func (r *coreRun) test(n coreNeed, c *coreFile) (match, more bool) {
	if !n.from.charge(1+(len(n.d.text)+len(c.vlnv.version))/
		coreTextStep, n.at) {
		return false, false
	}
	return n.d.matches(c.vlnv), true
}

// at returns the byte offset of n in its core, where the import stops
// when n runs the budget out.
func (n coreNeed) at() int { return n.d.off }

// matches reports whether the core have satisfies d.
func (d coreDep) matches(have vlnv) bool {
	w := d.want
	if w.vendor != have.vendor || w.library != have.library ||
		w.name != have.name {
		return false
	}
	if w.version == "" {
		return true
	}
	c := compareVersions(have.version, w.version)
	hp, wp := versionParts(have.version), versionParts(w.version)
	part := func(i int) bool {
		return len(hp) > i && len(wp) > i && hp[i] == wp[i]
	}
	switch d.op {
	case ">=":
		return c >= 0
	case ">":
		return c > 0
	case "<=":
		return c <= 0
	case "<":
		return c < 0
	case "^":
		return c >= 0 && part(0)
	case "~":
		return c >= 0 && part(0) && part(1)
	}
	return c == 0
}

// versionParts splits a version at '.' and '-'.
func versionParts(v string) []string {
	return strings.FieldsFunc(v, func(r rune) bool {
		return r == '.' || r == '-'
	})
}

// compareVersions compares two versions part by part: parts made of
// digits as numbers, others as strings; a version that runs out of
// parts first is the older.
func compareVersions(a, b string) int {
	ap, bp := versionParts(a), versionParts(b)
	for i := range min(len(ap), len(bp)) {
		x, y := ap[i], bp[i]
		if digits(x) && digits(y) {
			x, y = strings.TrimLeft(x, "0"), strings.TrimLeft(y, "0")
			if len(x) != len(y) {
				if len(x) < len(y) {
					return -1
				}
				return 1
			}
		}
		if c := strings.Compare(x, y); c != 0 {
			return c
		}
	}
	return len(ap) - len(bp)
}

func digits(s string) bool {
	for i := range len(s) {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return s != ""
}

// parseVLNV reads [op]vendor:library:name[:version].
func parseVLNV(s string) (op string, v vlnv, ok bool) {
	for _, o := range coreOps {
		if strings.HasPrefix(s, o) {
			op, s = o, s[len(o):]
			break
		}
	}
	parts := strings.Split(s, ":")
	if len(parts) < 3 || len(parts) > 4 || parts[2] == "" {
		return "", vlnv{}, false
	}
	v = vlnv{vendor: parts[0], library: parts[1], name: parts[2]}
	if len(parts) == 4 {
		v.version = parts[3]
	}
	return op, v, true
}

// readCore reads the .core file src at rel (relative to the project
// file's folder), shown as shown, spending from b.
func readCore(rel, shown string, src []byte, b *coreSpent) (c *coreFile) {
	c = &coreFile{rel: rel, l: newLines(shown, src), src: src, b: b,
		marks: map[int][]int{}}
	if b.bytes += len(src); b.bytes > coreByteBudget {
		c.over(0, "the .core files hold more than "+
			strconv.Itoa(coreByteBudget>>20)+" MiB")
		return c
	}
	defer func() {
		// The YAML library is third-party code; a panic inside it must
		// not crash GoHard (P6).
		if v := recover(); v != nil {
			c.fail(0, fmt.Sprint(v))
		}
	}()
	var doc yaml.Node
	if err := yaml.NewDecoder(bytes.NewReader(src)).Decode(
		&doc); err != nil {
		if errors.Is(err, io.EOF) {
			c.fail(0, "the file is empty")
		} else {
			c.syntax(err)
		}
		return c
	}
	if len(doc.Content) == 0 || doc.Content[0].Kind != yaml.MappingNode {
		c.fail(0, "the file is not a YAML mapping")
		return c
	}
	root := doc.Content[0]
	capi := false
	for i := 0; i+1 < len(root.Content); i += 2 {
		k, v := root.Content[i], deref(root.Content[i+1])
		if !c.step(k, v) {
			return c
		}
		switch k.Value {
		case "CAPI=2":
			capi = true
		case "name":
			if op, n, ok := parseVLNV(v.Value); ok && op == "" &&
				v.Kind == yaml.ScalarNode {
				c.vlnv, c.named = n, true
			}
		case "filesets":
			c.filesets(v)
		case "provider":
			c.ignored(k, "provider", "gohard import fetches nothing, "+
				"so it reads the files from the .core file's folder")
		}
	}
	if !capi {
		c.fail(0, "it is not a CAPI2 core: it has no CAPI=2: line")
	}
	return c
}

var yamlLineRE = regexp.MustCompile(`^yaml: line (\d+): `)

// syntax reports a YAML error; the library gives only its line.
func (c *coreFile) syntax(err error) {
	msg, line := err.Error(), 1
	if m := yamlLineRE.FindStringSubmatch(msg); m != nil {
		line, _ = strconv.Atoi(m[1])
		msg = msg[len(m[0]):]
	}
	msg = strings.TrimPrefix(msg, "yaml: ")
	line = max(1, min(line, c.l.idx.LineCount()))
	c.fail(c.l.idx.LineStart(line-1), msg)
}

// fail records the import/syntax error that makes c unusable.
func (c *coreFile) fail(off int, reason string) {
	c.fatal = true
	c.diags = append(c.diags, c.l.syntax(coreFormat, off, reason))
}

// over stops the import at off of c: a budget ran out.
func (c *coreFile) over(off int, reason string) {
	c.b.over = true
	c.b.stop = c.l.syntax(coreFormat, off, reason)
}

// charge spends n steps; at is where the import stops when that runs
// the budget out. It reports whether the import goes on.
func (c *coreFile) charge(n int, at func() int) bool {
	if c.b.over {
		return false
	}
	if c.b.steps += n; c.b.steps > coreBudget {
		c.over(at(), "the import takes more than "+
			strconv.Itoa(coreBudget)+" steps, counting each use of "+
			"a YAML alias")
		return false
	}
	return true
}

// step charges the item at n and its value v (nil for none): one step,
// plus one per coreTextStep bytes of their text. It reports whether
// the import goes on.
func (c *coreFile) step(n, v *yaml.Node) bool {
	text := len(deref(n).Value)
	if v != nil {
		text += len(deref(v).Value)
	}
	return c.charge(1+text/coreTextStep, func() int { return c.off(n) })
}

// ignored records the import/ignored hint for item at n.
func (c *coreFile) ignored(n *yaml.Node, item, reason string) {
	c.diags = append(c.diags, c.l.ignored(c.off(n), item, reason))
}

// deref returns the node an alias stands for.
func deref(n *yaml.Node) *yaml.Node {
	for i := 0; i < 8 && n.Kind == yaml.AliasNode && n.Alias != nil; i++ {
		n = n.Alias
	}
	return n
}

// pairs returns the keys and values of the mapping n, or nothing.
func pairs(n *yaml.Node) [][2]*yaml.Node {
	n = deref(n)
	if n.Kind != yaml.MappingNode {
		return nil
	}
	var out [][2]*yaml.Node
	for i := 0; i+1 < len(n.Content); i += 2 {
		out = append(out, [2]*yaml.Node{n.Content[i],
			deref(n.Content[i+1])})
	}
	return out
}

// yamlScalar returns the value of n when it is a scalar.
func yamlScalar(n *yaml.Node) (string, bool) {
	n = deref(n)
	return n.Value, n.Kind == yaml.ScalarNode
}

// filesets reads the filesets mapping.
func (c *coreFile) filesets(n *yaml.Node) {
	for _, set := range pairs(n) {
		if !c.step(set[0], nil) {
			return
		}
		var files, deps *yaml.Node
		typ, lib := "", "work"
		for _, kv := range pairs(set[1]) {
			if !c.step(kv[0], kv[1]) {
				return
			}
			switch kv[0].Value {
			case "files":
				files = kv[1]
			case "depend":
				deps = kv[1]
			case "file_type":
				typ, _ = yamlScalar(kv[1])
			case "logical_name":
				if v, ok := yamlScalar(kv[1]); ok && v != "" {
					lib = v
				}
			}
		}
		if files != nil && files.Kind == yaml.SequenceNode {
			for _, it := range files.Content {
				if !c.step(it, nil) || !c.file(it, typ, lib) {
					return
				}
			}
		}
		if deps != nil && deps.Kind == yaml.SequenceNode {
			for _, it := range deps.Content {
				if !c.step(it, nil) {
					return
				}
				c.depend(it)
			}
		}
	}
}

// file reads one files: entry, "path" or "path: {attributes}", with
// the fileset's file type and library; the files loop has charged the
// entry's step. It reports whether the import goes on.
func (c *coreFile) file(it *yaml.Node, typ, lib string) bool {
	at := deref(it)
	p, ok := yamlScalar(at)
	if !ok {
		m := at
		if m.Kind != yaml.MappingNode || len(m.Content) != 2 {
			c.ignored(it, "files entry", "not a file name or a file "+
				"name with attributes")
			return true
		}
		at = m.Content[0]
		if !c.step(at, nil) {
			return false
		}
		p = at.Value
		for _, a := range pairs(m.Content[1]) {
			if !c.step(a[0], a[1]) {
				return false
			}
			v, _ := yamlScalar(a[1])
			switch a[0].Value {
			case "file_type":
				typ = v
			case "logical_name":
				if v != "" {
					lib = v
				}
			}
		}
	}
	// Every entry reads its file type and library again (its message
	// may hold them), so their text counts for each entry.
	if !c.charge((len(typ)+len(lib))/coreTextStep, func() int {
		return c.off(at)
	}) {
		return false
	}
	p = strings.ReplaceAll(p, `\`, "/")
	std, vhdl := coreStandards[typ]
	switch {
	case coreFlagRE.MatchString(p):
		c.ignored(at, p, "it has a FuseSoC use-flag condition")
	case typ == "":
		c.ignored(at, p, "it has no file_type")
	case !vhdl:
		c.ignored(at, p, "file type "+typ+" is not VHDL")
	default:
		e, d := c.l.file(join(path.Dir(c.rel), p), lib, std, c.off(at))
		if d != nil {
			c.diags = append(c.diags, *d)
			return true
		}
		c.entries = append(c.entries, e)
	}
	return true
}

// depend reads one depend: entry.
func (c *coreFile) depend(it *yaml.Node) {
	s, ok := yamlScalar(it)
	switch {
	case !ok:
		c.ignored(it, "depend entry", "not a core name")
		return
	case coreFlagRE.MatchString(s):
		c.ignored(it, "depend "+s, "it has a FuseSoC use-flag condition")
		return
	}
	op, want, ok := parseVLNV(s)
	if !ok {
		c.ignored(it, "depend "+s, "not of the form "+
			"[op]vendor:library:name[:version]")
		return
	}
	c.deps = append(c.deps, coreDep{op: op, want: want, text: s,
		off: c.off(it)})
}

// off returns the byte offset of the node n. YAML counts columns in
// characters and does not count a byte order mark. Aliases send the
// nodes back and forth along a line, so off keeps the byte offset of
// every coreMark-th character of each line it converts: a line costs
// linear time once, then each node at most coreMark characters (P7).
func (c *coreFile) off(n *yaml.Node) int {
	idx := c.l.idx
	line := min(max(1, n.Line), idx.LineCount())
	end := idx.LineEnd(line - 1)
	marks, ok := c.marks[line]
	if !ok {
		at := idx.LineStart(line - 1)
		if line == 1 && bytes.HasPrefix(c.src, utf8BOM) {
			at += len(utf8BOM)
		}
		marks = []int{at}
		for k := 1; at < end; k++ {
			_, size := utf8.DecodeRune(c.src[at:end])
			at += size
			if k%coreMark == 0 {
				marks = append(marks, at)
			}
		}
		c.marks[line] = marks
	}
	k := max(1, n.Column) - 1 // the characters before n
	i := min(k/coreMark, len(marks)-1)
	off := marks[i]
	for k -= i * coreMark; k > 0 && off < end; k-- {
		_, size := utf8.DecodeRune(c.src[off:end])
		off += size
	}
	return off
}
