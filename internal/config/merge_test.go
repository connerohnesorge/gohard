package config

import (
	"slices"
	"strings"
	"testing"

	"github.com/connerohnesorge/gohard/internal/diag"
	"github.com/connerohnesorge/gohard/internal/env"
)

func TestApplyDefines(t *testing.T) {
	c, ds := Parse(FileName, []byte("defines:\n  FOO: \"0\"\n"+
		"  Bar: \"b\"\n"))
	if len(ds) != 0 {
		t.Fatal(ds)
	}
	got, logs, ds := ApplyDefines(c, []string{"foo=1", "NEW=x=y",
		"FOO=2", "Z="})
	if len(ds) != 0 {
		t.Fatal(ds)
	}
	wantLogs := []string{`--define foo=1 overrides defines.FOO: "0" ` +
		`(gohard.yaml:2:3)`, `--define FOO=2 overrides --define foo=1`}
	if !slices.Equal(logs, wantLogs) {
		t.Errorf("logs %q, want %q", logs, wantLogs)
	}
	if show(got.Defines) != `{Bar@3:3: "b", FOO@0:0: "2", `+
		`NEW@0:0: "x=y", Z@0:0: ""}` {
		t.Errorf("defines %s", show(got.Defines))
	}
	if v, _ := got.Define("foo"); v != "2" {
		t.Errorf("Define(foo) = %q", v)
	}
	if show(c.Defines) != `{Bar@3:3: "b", FOO@2:3: "0"}` {
		t.Errorf("ApplyDefines modified its input: %s", show(c.Defines))
	}
	_, logs, ds = ApplyDefines(c, []string{"NOEQ", "9x=1",
		"vhdl_version=2008", "TOOL_NAME=x"})
	if len(logs) != 0 {
		t.Errorf("logs %q", logs)
	}
	var codes []diag.Code
	for _, d := range ds {
		codes = append(codes, d.Code)
	}
	want := []diag.Code{diag.ConfigBuiltinDefine, diag.ConfigBuiltinDefine,
		diag.CLIUsage, diag.CLIUsage}
	slices.Sort(codes)
	slices.Sort(want)
	if !slices.Equal(codes, want) {
		t.Errorf("codes %v, want %v", codes, want)
	}
	if diag.ExitCode(ds, false, diag.NoSignal) != diag.ExitTool {
		t.Error("bad --define does not exit 2")
	}
}

// TestApplyDefinesLaw: the result holds, for every name, the value of
// the last --define for it, else the file's value; every --define that
// replaces a file entry or an earlier --define is logged, once.
func TestApplyDefinesLaw(t *testing.T) {
	r := env.SplitMix64{State: 5}
	names := []string{"A", "a", "B", "C", "d"}
	for range 2000 {
		var src strings.Builder
		src.WriteString("defines:\n")
		file := map[string]string{}
		for _, n := range names {
			if env.Intn(&r, 2) == 0 || file[strings.ToUpper(n)] != "" {
				continue
			}
			v := "f" + n
			file[strings.ToUpper(n)] = v
			src.WriteString("  " + n + ": " + v + "\n")
		}
		c, ds := Parse(FileName, []byte(src.String()))
		if len(ds) != 0 && len(file) > 0 {
			t.Fatal(ds)
		}
		var args []string
		last := map[string]string{}
		var replaced []string
		seen := map[string]bool{}
		for range env.Intn(&r, 5) {
			n := names[env.Intn(&r, len(names))]
			v := "c" + strings.Repeat("x", env.Intn(&r, 3))
			args = append(args, n+"="+v)
			u := strings.ToUpper(n)
			if _, ok := file[u]; ok || seen[u] {
				replaced = append(replaced, u)
			}
			seen[u] = true
			last[u] = v
		}
		got, logs, ds := ApplyDefines(c, args)
		if len(ds) != 0 {
			t.Fatal(ds)
		}
		if len(logs) != len(replaced) {
			t.Fatalf("args %v file %v: logs %q, want %d", args, file,
				logs, len(replaced))
		}
		for _, n := range []string{"A", "B", "C", "D"} {
			want, ok := last[n]
			if !ok {
				want, ok = file[n]
			}
			v, has := got.Define(n)
			if has != ok || v != want {
				t.Fatalf("args %v file %v: %s = %q, %v; want %q, %v",
					args, file, n, v, has, want, ok)
			}
		}
		// Config.Defines stays sorted by name and unique, so output
		// built from it never depends on the --define order.
		if !slices.IsSortedFunc(got.Defines, func(a, b Define) int {
			return identLess(a.Name, b.Name)
		}) {
			t.Fatalf("args %v: Defines not sorted: %+v", args,
				got.Defines)
		}
		for i := 1; i < len(got.Defines); i++ {
			if identLess(got.Defines[i-1].Name, got.Defines[i].Name) == 0 {
				t.Fatalf("args %v: duplicate define %+v", args,
					got.Defines)
			}
		}
	}
}

// TestMergeEditor checks the merge law on every subset pattern: for
// each key, the merged value is the file's if the file sets it, else
// the editor's if the editor sets it, else the default; overridden is
// exactly the keys both set, in table order.
func TestMergeEditor(t *testing.T) {
	r := env.SplitMix64{State: 9}
	defaults := Defaults()
	for range 1000 {
		fileVals, editorVals := map[string]string{}, map[string]string{}
		var fileOrder, editorOrder, both []string
		for _, k := range keys {
			inFile, inEditor := env.Intn(&r, 2) == 0, env.Intn(&r, 2) == 0
			if inFile {
				fileVals[k.Name], _ = genNonDefault(&r, k)
				fileOrder = append(fileOrder, k.Name)
			}
			if inEditor {
				editorVals[k.Name], _ = genNonDefault(&r, k)
				editorOrder = append(editorOrder, k.Name)
			}
			if inFile && inEditor {
				both = append(both, k.Name)
			}
		}
		file, ds := Parse(FileName, []byte(renderKeys(fileOrder,
			fileVals)))
		if len(ds) != 0 {
			t.Fatal(ds)
		}
		editor, ds := Parse("settings", []byte(renderKeys(editorOrder,
			editorVals)))
		if len(ds) != 0 {
			t.Fatal(ds)
		}
		m, over := MergeEditor(file, editor)
		if !slices.Equal(over, both) {
			t.Fatalf("overridden %v, want %v", over, both)
		}
		for _, k := range keys {
			want := plain(defaults.Value(k.Name))
			switch {
			case file.IsSet(k.Name):
				want = plain(file.Value(k.Name))
			case editor.IsSet(k.Name):
				want = plain(editor.Value(k.Name))
			}
			if got := plain(m.Value(k.Name)); got != want {
				t.Fatalf("%s = %s, want %s", k.Name, got, want)
			}
			if m.IsSet(k.Name) != (file.IsSet(k.Name) ||
				editor.IsSet(k.Name)) {
				t.Fatalf("%s: IsSet wrong", k.Name)
			}
		}
		if file.SetKeys() == nil && len(fileOrder) > 0 {
			t.Fatal("file lost its set keys")
		}
	}
	// The editor's settings arrive as JSON, which Parse reads.
	file, _ := Parse(FileName, []byte("format:\n  indent: 4\n"))
	editor, ds := Parse("settings", []byte(`{"format": {"indent": 8, `+
		`"keyword_case": "upper"}, "lenient": true}`))
	if len(ds) != 0 {
		t.Fatal(ds)
	}
	m, over := MergeEditor(file, editor)
	if !slices.Equal(over, []string{"format.indent"}) ||
		m.Format.Indent != 4 || m.Format.KeywordCase != "upper" ||
		!m.Lenient {
		t.Errorf("merge = %+v, overridden %v", m, over)
	}
	if OverrideMessage("format.indent") != "gohard.yaml sets "+
		"format.indent; the editor setting format.indent is ignored" {
		t.Error(OverrideMessage("format.indent"))
	}
}

// genNonDefault returns a valid value for k that differs from the
// default.
func genNonDefault(r *env.SplitMix64, k Key) (string, string) {
	defaults := Defaults()
	def := plain(defaults.Value(k.Name))
	for {
		text, shown := genValue(r, k)
		if shown != def {
			return text, shown
		}
	}
}
