package cli_test

import (
	"bytes"
	"context"
	"flag"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/connerohnesorge/gohard/internal/cli"
	"github.com/connerohnesorge/gohard/internal/env"
	"github.com/connerohnesorge/gohard/internal/env/realenv"
	"github.com/connerohnesorge/gohard/internal/libs"
	"github.com/connerohnesorge/gohard/internal/stdlib"
)

var update = flag.Bool("update", false,
	"rewrite testdata/cli want files from the -p 1 run")

const fixtureRoot = "../../testdata/cli"

// jobCounts are the -p values every fixture runs at; their outputs must be
// byte-identical (#20, #30).
var jobCounts = []int{1, 16}

// TestCLIFixtures runs every testdata/cli/<case> in-process through
// cli.Run with a real Env. The format is described in
// testdata/cli/README.md.
func TestCLIFixtures(t *testing.T) {
	cases, err := os.ReadDir(fixtureRoot)
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, c := range cases {
		if !c.IsDir() {
			continue
		}
		n++
		dir := filepath.Join(fixtureRoot, c.Name())
		t.Run(c.Name(), func(t *testing.T) {
			t.Parallel()
			runFixture(t, dir, cli.Run)
		})
	}
	if n == 0 {
		t.Fatal("no fixtures under " + fixtureRoot)
	}
}

// tb is the part of testing.TB the harness uses, so the harness
// self-test can run it against a recording fake.
type tb interface {
	Helper()
	Fatal(args ...any)
	Fatalf(format string, args ...any)
	Errorf(format string, args ...any)
	Skip(args ...any)
	TempDir() string
}

// runner is the signature of cli.Run.
type runner func(ctx context.Context, e env.Env, args []string) int

type outcome struct {
	exit           int
	stdout, stderr []byte
	tree           map[string][]byte
}

func runFixture(t tb, dir string, run runner) {
	raw, err := os.ReadFile(filepath.Join(dir, "cmd"))
	if err != nil {
		t.Fatal(err)
	}
	words, err := commandWords(string(raw))
	if err != nil {
		t.Fatalf("cmd: %v", err)
	}
	stdin, err := os.ReadFile(filepath.Join(dir, "stdin"))
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	_, statErr := os.Stat(filepath.Join(dir, "want"))
	checkTree := statErr == nil
	var first outcome
	for i, p := range jobCounts {
		args := append([]string{"-p", strconv.Itoa(p)}, words[1:]...)
		got := runOnce(t, dir, args, stdin, run)
		if i == 0 {
			first = got
			continue
		}
		if got.exit != first.exit ||
			!bytes.Equal(got.stdout, first.stdout) ||
			!bytes.Equal(got.stderr, first.stderr) ||
			checkTree && !sameTree(got.tree, first.tree) {
			t.Fatalf("-p %d differs from -p %d:\nexit %d vs %d\n"+
				"stdout:\n%s\nvs\n%s\nstderr:\n%s\nvs\n%s",
				p, jobCounts[0], got.exit, first.exit, got.stdout,
				first.stdout, got.stderr, first.stderr)
		}
	}
	if *update {
		write(t, filepath.Join(dir, "want.exit"),
			[]byte(strconv.Itoa(first.exit)+"\n"))
		write(t, filepath.Join(dir, "want.stdout"), first.stdout)
		write(t, filepath.Join(dir, "want.stderr"), first.stderr)
		if checkTree {
			want := filepath.Join(dir, "want")
			if err := os.RemoveAll(want); err != nil {
				t.Fatal(err)
			}
			writeTree(t, want, first.tree)
		}
		return
	}
	wantExit := strings.TrimSpace(string(read(t,
		filepath.Join(dir, "want.exit"))))
	if strconv.Itoa(first.exit) != wantExit {
		t.Errorf("exit = %d, want %s", first.exit, wantExit)
	}
	compare(t, "stdout", first.stdout,
		read(t, filepath.Join(dir, "want.stdout")))
	compare(t, "stderr", first.stderr,
		read(t, filepath.Join(dir, "want.stderr")))
	if checkTree {
		want := readTree(t, filepath.Join(dir, "want"))
		if !sameTree(first.tree, want) {
			t.Errorf("project tree after the run:\n%s\nwant:\n%s",
				treeString(first.tree), treeString(want))
		}
	}
}

// runOnce copies in/ to a fresh temp dir, runs args there, and returns
// the outcome with the temp dir replaced by $WORK in both streams.
func runOnce(t tb, dir string, args []string,
	stdin []byte, run runner,
) outcome {
	work := t.TempDir()
	if in := filepath.Join(dir, "in"); exists(in) {
		if err := os.CopyFS(work, os.DirFS(in)); err != nil {
			t.Fatal(err)
		}
	}
	st := applySetup(t, dir, work)
	var stdout, stderr bytes.Buffer
	e := realenv.New(realenv.Options{
		Stdin: bytes.NewReader(stdin), Stdout: &stdout, Stderr: &stderr,
		Dir: filepath.Join(work, filepath.FromSlash(st.cwd)),
	})
	ctx, cancel := context.WithCancelCause(context.Background())
	defer cancel(nil)
	// The bundled std/ieee files the language server writes for its
	// locations (#33) go to a cache of this run, shown as $BUNDLE.
	cache := t.TempDir()
	getenv := e.Getenv
	e.Getenv = func(k string) (string, bool) {
		if k == "XDG_CACHE_HOME" {
			return cache, true
		}
		return getenv(k)
	}
	bundle := filepath.Join(cache, "gohard", "bundle", stdlib.CacheKey())
	defer unlockTree(bundle)
	st.wrap(&e, cancel)
	if st.hostOS != "" {
		ctx = libs.WithHost(ctx, st.hostOS, st.hostRoot)
	}
	if b := st.build; b != nil {
		ctx = cli.WithBuildInfo(ctx, b.version, b.commit, b.bi)
	}
	for _, p := range st.tamper {
		ctx = stdlib.WithTamper(ctx, p)
	}
	for _, p := range st.bundleErr {
		ctx = stdlib.WithInternalError(ctx, p)
	}
	exit := run(ctx, e, args)
	st.restorePerms(t)
	clean := func(b []byte) []byte {
		// A Windows path in a URI is file:///C:/...; show it as
		// file://$TOKEN like a Unix path.
		for w, tok := range map[string]string{
			bundle: "$BUNDLE", work: "$WORK",
		} {
			if filepath.VolumeName(w) != "" {
				b = bytes.ReplaceAll(b,
					[]byte("file:///"+filepath.ToSlash(w)),
					[]byte("file://"+tok))
			}
		}
		for _, w := range []string{bundle, filepath.ToSlash(bundle)} {
			b = bytes.ReplaceAll(b, []byte(w), []byte("$BUNDLE"))
		}
		for _, w := range []string{work, filepath.ToSlash(work)} {
			b = bytes.ReplaceAll(b, []byte(w), []byte("$WORK"))
		}
		return b
	}
	return outcome{
		exit:   exit,
		stdout: clean(stdout.Bytes()),
		stderr: clean(stderr.Bytes()),
		tree:   readTree(t, work),
	}
}

// unlockTree makes the read-only files under dir writable again, so
// the temp dir can be removed on every OS.
func unlockTree(dir string) {
	_ = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			_ = os.Chmod(p, 0o644)
		}
		return nil
	})
}

// commandWords splits the first non-blank, non-comment line of a cmd
// file into words: whitespace separates words, '...' is literal, and
// "..." allows \" and \\ escapes. The first word must be gohard.
func commandWords(src string) ([]string, error) {
	var line string
	for l := range strings.Lines(src) {
		l = strings.TrimSpace(l)
		if l != "" && !strings.HasPrefix(l, "#") {
			line = l
			break
		}
	}
	var (
		words []string
		cur   strings.Builder
		in    bool // inside a word
		quote rune
	)
	rs := []rune(line)
	for i := 0; i < len(rs); i++ {
		r := rs[i]
		switch {
		case quote == '\'':
			if r == '\'' {
				quote = 0
			} else {
				cur.WriteRune(r)
			}
		case quote == '"':
			switch {
			case r == '"':
				quote = 0
			case r == '\\' && i+1 < len(rs) &&
				(rs[i+1] == '"' || rs[i+1] == '\\'):
				i++
				cur.WriteRune(rs[i])
			default:
				cur.WriteRune(r)
			}
		case r == '\'' || r == '"':
			quote, in = r, true
		case r == ' ' || r == '\t':
			if in {
				words = append(words, cur.String())
				cur.Reset()
				in = false
			}
		default:
			cur.WriteRune(r)
			in = true
		}
	}
	if quote != 0 {
		return nil, fmt.Errorf("unterminated %c quote", quote)
	}
	if in {
		words = append(words, cur.String())
	}
	if len(words) == 0 || words[0] != "gohard" {
		return nil, fmt.Errorf("command must start with gohard: %q", line)
	}
	return words, nil
}

func TestCommandWords(t *testing.T) {
	tests := []struct {
		src  string
		want []string
	}{
		{"gohard help\n", []string{"gohard", "help"}},
		{"# comment\n\n  gohard  check  a.vhd \n",
			[]string{"gohard", "check", "a.vhd"}},
		{`gohard check --define 'A=x y' "B=\"q\"" ''`,
			[]string{"gohard", "check", "--define", "A=x y",
				`B="q"`, ""}},
	}
	for _, tt := range tests {
		got, err := commandWords(tt.src)
		if err != nil || !slices.Equal(got, tt.want) {
			t.Errorf("commandWords(%q) = %q, %v; want %q",
				tt.src, got, err, tt.want)
		}
	}
	for _, bad := range []string{"", "check", "gohard 'x"} {
		if _, err := commandWords(bad); err == nil {
			t.Errorf("commandWords(%q) accepted", bad)
		}
	}
}

func compare(t tb, name string, got, want []byte) {
	t.Helper()
	if !bytes.Equal(got, want) {
		t.Errorf("%s:\n%s\nwant:\n%s", name, got, want)
	}
}

func exists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

func read(t tb, p string) []byte {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("%v (run go test -update to create it)", err)
	}
	return b
}

func write(t tb, p string, b []byte) {
	t.Helper()
	if err := os.WriteFile(p, b, 0o644); err != nil {
		t.Fatal(err)
	}
}

// readTree returns every regular file under root by slash path.
func readTree(t tb, root string) map[string][]byte {
	t.Helper()
	tree := map[string][]byte{}
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry,
		err error,
	) error {
		if err != nil || d.IsDir() || d.Type()&fs.ModeSymlink != 0 {
			return err
		}
		rel, _ := filepath.Rel(root, p)
		b, err := os.ReadFile(p)
		tree[filepath.ToSlash(rel)] = b
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return tree
}

func writeTree(t tb, root string, tree map[string][]byte) {
	t.Helper()
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range sortedKeys(tree) {
		p := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		write(t, p, tree[name])
	}
}

func sameTree(a, b map[string][]byte) bool {
	return treeString(a) == treeString(b)
}

func treeString(tree map[string][]byte) string {
	var b strings.Builder
	for _, name := range sortedKeys(tree) {
		fmt.Fprintf(&b, "== %s\n%s", name, tree[name])
	}
	return b.String()
}

func sortedKeys(m map[string][]byte) []string {
	return slices.Sorted(maps.Keys(m))
}
