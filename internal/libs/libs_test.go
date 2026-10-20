package libs

import (
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"

	"github.com/connerohnesorge/gohard/internal/config"
	"github.com/connerohnesorge/gohard/internal/diag"
	"github.com/connerohnesorge/gohard/internal/env"
	"github.com/connerohnesorge/gohard/internal/env/realenv"
)

// readOnly is an env.FS that fails the test on any write: the search
// never changes a file (#1).
type readOnly struct {
	env.FS
	t *testing.T
}

func (r readOnly) fail(op, name string) error {
	r.t.Errorf("search wrote: %s %s", op, name)
	return fs.ErrPermission
}

func (r readOnly) WriteFile(n string, _ []byte, _ fs.FileMode) error {
	return r.fail("WriteFile", n)
}

func (r readOnly) WriteTemp(d, _ string, _ []byte) (string, error) {
	return "", r.fail("WriteTemp", d)
}
func (r readOnly) Rename(a, _ string) error { return r.fail("Rename", a) }
func (r readOnly) Chmod(n string, _ fs.FileMode) error {
	return r.fail("Chmod", n)
}
func (r readOnly) Remove(n string) error { return r.fail("Remove", n) }
func (r readOnly) MkdirAll(n string, _ fs.FileMode) error {
	return r.fail("MkdirAll", n)
}

func (r readOnly) Create(n string) (io.WriteCloser, error) {
	return nil, r.fail("Create", n)
}

func (r readOnly) Append(n string) (io.WriteCloser, error) {
	return nil, r.fail("Append", n)
}

const plain = "package vcomponents is\nend package;\n"

const envelope = "entity secret is\nend entity;\n" +
	"`protect begin_protected\n`protect data_block\nAAAA\n" +
	"`protect end_protected\n"

// tree writes files (slash paths relative to dir) and returns a host
// rooted at dir.
func tree(t *testing.T, goos string, vars map[string]string,
	files map[string]string,
) Host {
	t.Helper()
	dir := t.TempDir()
	for p, text := range files {
		full := filepath.Join(dir, filepath.FromSlash(p))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	e := realenv.New(realenv.Options{Dir: dir})
	return Host{OS: goos, FS: readOnly{e.FS, t}, Root: dir,
		Getenv: func(k string) (string, bool) {
			v, ok := vars[k]
			return v, ok
		}}
}

func vivado(base, ver string) map[string]string {
	d := base + "/Vivado/" + ver + "/data"
	return map[string]string{
		d + "/vhdl/src/unisims/unisim_VCOMP.vhd":    plain,
		d + "/vhdl/src/unisims/secureip/secret.vhd": envelope,
		d + "/vhdl/src/unimacro/unimacro_VCOMP.vhd": plain,
		d + "/secureip/a.vhd":                       envelope,
	}
}

func merge(ms ...map[string]string) map[string]string {
	out := map[string]string{}
	for _, m := range ms {
		for k, v := range m {
			out[k] = v
		}
	}
	return out
}

// TestHostPaths: host paths survive the trip to an FS name and back
// on every OS, and relative or foreign paths are rejected.
func TestHostPaths(t *testing.T) {
	root := t.TempDir()
	cases := map[string][]string{
		"linux": {"/", "/opt/Xilinx", "/a b/c'd", "/x/./y/../z"},
		"darwin": {"/Applications/Xilinx/2025.1/Vivado",
			"/Users/u/intelFPGA lite"},
		"windows": {`C:\Xilinx`, `c:\Program Files\GHDL\lib\ghdl`,
			`D:/mixed\seps`, `C:\`},
	}
	for goos, ps := range cases {
		h := Host{OS: goos, Root: root}
		for _, p := range ps {
			s := h.slash(p)
			if s == "" {
				t.Fatalf("%s: slash(%q) rejected", goos, p)
			}
			if back := h.fromFS(h.fsPath(s)); back != s {
				t.Errorf("%s: %q -> %q -> %q", goos, p, h.fsPath(s), back)
			}
			if h.slash(h.Show(s)) != s {
				t.Errorf("%s: Show(%q) = %q does not read back", goos,
					s, h.Show(s))
			}
		}
	}
	for goos, p := range map[string]string{"linux": "rel/x",
		"windows": `rel\x`, "darwin": `C:\x`} {
		if s := (Host{OS: goos}).slash(p); s != "" {
			t.Errorf("%s: slash(%q) = %q, want rejected", goos, p, s)
		}
	}
}

// TestQuote: every folder, whatever its characters, reads back from
// the YAML line exactly (Windows drive letters, backslashes, spaces,
// quotes, # and :).
func TestQuote(t *testing.T) {
	alphabet := []string{`C:\`, `\`, "/", " ", "'", `"`, "#", ": ", "a",
		"[", "]", "{", ",", "&", "*", "!", "%", "@", "`", "é", "-"}
	n := 0
	for i := range alphabet {
		for j := range alphabet {
			for k := range alphabet {
				s := alphabet[i] + alphabet[j] + "x" + alphabet[k]
				n++
				l := Library{Name: "lib", Folder: s}
				var got map[string][]string
				if err := yaml.Unmarshal([]byte(l.Entry()),
					&got); err != nil {
					t.Fatalf("%q: %v", l.Entry(), err)
				}
				if !reflect.DeepEqual(got["lib"], []string{s}) {
					t.Fatalf("%q reads back as %q", s, got["lib"])
				}
			}
		}
	}
	if n < 1000 {
		t.Fatal("too few cases")
	}
}

// TestDetectProducts finds each vendor through its usual folder, its
// variable and its program on PATH, on every OS.
func TestDetectProducts(t *testing.T) {
	cases := []struct {
		name, goos string
		vars       map[string]string
		files      map[string]string
		want       string // product version via
	}{
		{"vivado-usual", "linux", nil, vivado("opt/Xilinx", "2023.2"),
			"vivado 2023.2 the usual install folder"},
		{"vivado-home", "linux", map[string]string{"HOME": "/home/u"},
			vivado("home/u/Xilinx", "2022.2"),
			"vivado 2022.2 the usual install folder"},
		{"vivado-env", "windows", map[string]string{
			"XILINX_VIVADO": `D:\tools\Vivado\2024.2`},
			vivado("D/tools", "2024.2"), "vivado 2024.2 XILINX_VIVADO"},
		{"vivado-path", "darwin", map[string]string{
			"PATH": "/usr/bin:/mnt/Vivado/2023.1/bin"},
			merge(vivado("mnt", "2023.1"), map[string]string{
				"mnt/Vivado/2023.1/bin/vivado": ""}),
			"vivado 2023.1 vivado on PATH"},
		{"quartus-bat", "windows", map[string]string{
			"PATH": `C:\x;"C:\q\21.1\quartus\bin64"`},
			map[string]string{
				"C/q/21.1/quartus/eda/sim_lib/a.vhd":    plain,
				"C/q/21.1/quartus/bin64/quartus_sh.bat": ""},
			"quartus 21.1 quartus_sh on PATH"},
		{"diamond-foundry", "linux", map[string]string{
			"FOUNDRY": "/srv/diamond/3.11/ispfpga"},
			map[string]string{
				"srv/diamond/3.11/cae_library/simulation/vhdl/" +
					"ecp5u/src/a.vhd": plain},
			"diamond 3.11 FOUNDRY"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h := tree(t, c.goos, c.vars, c.files)
			r := Detect(h, "")
			if len(r.Installs) != 1 {
				t.Fatalf("installs = %+v", r.Installs)
			}
			in := r.Installs[0]
			if got := in.Product + " " + in.Version + " " +
				in.Via; got != c.want {
				t.Errorf("got %q, want %q", got, c.want)
			}
			if len(in.Libraries) == 0 {
				t.Error("no libraries")
			}
		})
	}
}

// TestDetectClassifies: plain folders are mapped with their encrypted
// units listed for allow_opaque, encrypted and precompiled folders are
// unreadable, stubs are never mappable.
func TestDetectClassifies(t *testing.T) {
	h := tree(t, "linux", nil, merge(vivado("opt/Xilinx", "2023.2"),
		map[string]string{
			"opt/intelFPGA/23.1/quartus/eda/sim_lib/a.vhd": plain,
			"opt/intelFPGA/23.1/modelsim_ase/altera/vhdl/" +
				"altera_mf/_info": "",
		}))
	got := map[string]Library{}
	for _, in := range Detect(h, "").Installs {
		for _, l := range in.Libraries {
			got[in.Product+"/"+l.Name+"/"+l.Folder] = l
		}
	}
	want := map[string]string{
		"vivado/unisim/":   "",
		"vivado/unimacro/": "",
		"vivado/secureip/": "IEEE 1735 encrypted IP",
		"quartus/altera_mf/" +
			"/opt/intelFPGA/23.1/quartus/eda/sim_lib": "",
		"quartus/altera_mf/" +
			"/opt/intelFPGA/23.1/modelsim_ase/altera/vhdl/altera_mf": "precompiled for ModelSim/Questa",
	}
	for prefix, why := range want {
		found := false
		for k, l := range got {
			if strings.HasPrefix(k, prefix) {
				found = true
				if l.Unreadable != why {
					t.Errorf("%s: unreadable %q, want %q", k,
						l.Unreadable, why)
				}
			}
		}
		if !found {
			t.Errorf("no library %s in %v", prefix, got)
		}
	}
	for k, l := range got {
		if strings.HasPrefix(k, "vivado/unisim/") &&
			!slices.Equal(l.Opaque, []string{"unisim.secret"}) {
			t.Errorf("unisim opaque = %v", l.Opaque)
		}
	}
}

// TestStubsNeverMapped: wherever a stub sits and however it is found
// (path, marker line, GHDL index), it never gets a config line.
func TestStubsNeverMapped(t *testing.T) {
	marked := StubMarker + "\n" + plain
	layouts := []map[string]string{
		{"opt/Xilinx/Vivado/1.0/data/vhdl/src/unisims/a.vhd": marked},
		{"home/u/p/testdata/stubs/x/a.vhd": plain,
			"home/u/p/b/x-obj08.cf": "v 4\nfile \"../testdata/stubs/x/\" " +
				"\"a.vhd\" \"0\" \"0\":\n"},
		{"home/u/p/copy/a.vhd": marked,
			"home/u/p/b/y-obj93.cf": "v 4\nfile . \"../copy/a.vhd\" " +
				"\"0\" \"0\":\n"},
	}
	for i, files := range layouts {
		h := tree(t, "linux", nil, files)
		root := h.fsPath("/home/u/p")
		r := Detect(h, root)
		if len(r.Installs) == 0 {
			t.Fatalf("layout %d: nothing found", i)
		}
		plan, err := r.Choose(nil)
		if err != nil {
			t.Fatal(err)
		}
		for _, g := range plan.Groups {
			if len(g.Entries) > 0 {
				t.Errorf("layout %d: stub mapped: %+v", i, g.Entries)
			}
		}
		if strings.Contains(plan.Text(), "\n  x:") ||
			strings.Contains(plan.Text(), "[") {
			t.Errorf("layout %d: text maps a stub:\n%s", i, plan.Text())
		}
		if got := Find(h, root, "x"); len(got) != 0 {
			t.Errorf("layout %d: Find offers a stub: %+v", i, got)
		}
	}
}

// TestGHDLIndex follows every form of GHDL's file line back to its
// source folder.
func TestGHDLIndex(t *testing.T) {
	h := tree(t, "linux", map[string]string{
		"GHDL_PREFIX": "/g/lib/ghdl"}, map[string]string{
		"src/u/a.vhd":   plain,
		"src/u/p/b.vhd": plain,
		"g/lib/ghdl/vendors/x/unisim/v08/unisim-obj08.cf": "v 4\n" +
			"file \"/src/u/\" \"a.vhd\" \"0\" \"0\":\n" +
			"  package vcomponents at 1( 0) + 0 on 4;\n" +
			"file / \"/src/u/p/b.vhd\" \"0\" \"0\":\n",
		"g/lib/ghdl/vendors/x/rel/v08/rel-obj08.cf": "v 4\r\n" +
			"file . \"../../../../../../../src/u/a.vhd\" \"0\" \"0\":\r\n",
		"g/lib/ghdl/vendors/x/none/v08/none-obj08.cf": "v 4\n",
		"g/lib/ghdl/vendors/x/ieee/v08/ieee-obj08.cf": "v 4\n",
	})
	got := map[string]string{}
	for _, in := range Detect(h, "").Installs {
		for _, l := range in.Libraries {
			got[l.Name] = l.Folder + "|" + l.Unreadable
		}
	}
	want := map[string]string{
		"unisim": "/src/u|",
		"rel":    "/src/u|",
		"none": "/g/lib/ghdl/vendors/x/none/v08|precompiled for GHDL " +
			"without its sources",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v\nwant %v", got, want)
	}
}

// TestChoose: one install needs no pick; several need --version; a
// pick that matches nothing is an error; the same library through two
// installs is mapped once.
func TestChoose(t *testing.T) {
	h := tree(t, "linux", nil, merge(vivado("opt/Xilinx", "2023.2"),
		vivado("opt/Xilinx", "2023.10")))
	r := Detect(h, "")
	if v := []string{
		r.Installs[0].Version,
		r.Installs[1].Version,
	}; !slices.Equal(
		v,
		[]string{"2023.2", "2023.10"},
	) {
		t.Fatalf("versions not in numeric order: %v", v)
	}
	plan, err := r.Choose(nil)
	if err != nil || len(plan.Ambiguous) != 1 || len(plan.Groups) != 0 {
		t.Fatalf("no pick: %+v %v", plan, err)
	}
	for _, pick := range []string{"2023.10", "vivado=2023.10"} {
		plan, err = r.Choose([]string{pick})
		if err != nil || len(plan.Groups) != 1 ||
			plan.Groups[0].Install.Version != "2023.10" {
			t.Fatalf("%s: %+v %v", pick, plan, err)
		}
	}
	for _, bad := range []string{"1.0", "quartus=2023.2", "nope=1",
		"vivado="} {
		if _, err := r.Choose([]string{bad}); err == nil {
			t.Errorf("--version %s accepted", bad)
		}
	}
	var lines map[string]any
	if err := yaml.Unmarshal([]byte(plan.Text()), &lines); err != nil {
		t.Fatalf("Text is not YAML: %v\n%s", err, plan.Text())
	}
	if _, ok := lines["libraries"].(map[string]any)["unisim"]; !ok {
		t.Errorf("Text lacks unisim:\n%s", plan.Text())
	}
}

// TestVersionOrder: compareVersions is a total order consistent with
// numbers.
func TestVersionOrder(t *testing.T) {
	vs := []string{"2023.2", "2023.10", "2024.1", "3.13", "3.9", "22.1std",
		"22.1", "21.1.1", "a", ""}
	for _, a := range vs {
		for _, b := range vs {
			ab, ba := compareVersions(a, b), compareVersions(b, a)
			if ab != -ba || (ab == 0) != (a == b) {
				t.Errorf("compare(%q,%q)=%d, reverse %d", a, b, ab, ba)
			}
			for _, c := range vs {
				if ab < 0 && compareVersions(b, c) < 0 &&
					compareVersions(a, c) >= 0 {
					t.Errorf("not transitive: %q %q %q", a, b, c)
				}
			}
		}
	}
}

// TestEdits: whatever shape gohard.yaml has, the libraries and
// allow_opaque edits give a valid config that keeps every old key and
// adds the new entries.
func TestEdits(t *testing.T) {
	shapes := []string{
		"",
		"standard: \"2019\"",
		"standard: \"2019\"\n",
		"libraries:\n  ip: [ip]\n",
		"libraries: # mine\n    ip: [ip]\nallow_opaque: []\n",
		"libraries: {}\nallow_opaque: [ip.a]\n",
		"allow_opaque:\n  - ip.a\n\nlibraries:\n  ip: [ip]\n",
		"allow_opaque:\n- ip.a\nstandard: \"2019\"\r\n",
		"libraries:\n  ip: [ip]",
		"allow_opaque:",
	}
	lines := []string{"# from detect", "unisim: ['C:\\X Y\\unisims']",
		"unimacro: ['/opt/a b']"}
	units := []string{"unisim.secret", "unisim.other"}
	for _, s := range shapes {
		text := []byte(s)
		e, ok := LibrariesEdit(text, lines...)
		if !ok {
			t.Fatalf("%q: libraries edit refused", s)
		}
		text = e.Apply(text)
		e, ok = OpaqueEdit(text, units)
		if !ok {
			t.Fatalf("%q: allow_opaque edit refused:\n%s", s, text)
		}
		text = e.Apply(text)
		c, ds := config.Parse(config.FileName, text)
		for _, d := range ds {
			if d.Severity == diag.Error || d.Class() == diag.Tool {
				t.Fatalf("%q -> invalid:\n%s\n%v", s, text, ds)
			}
		}
		var names []string
		for _, l := range c.Libraries {
			names = append(names, l.Name)
		}
		for _, want := range []string{"unisim", "unimacro"} {
			if !slices.Contains(names, want) {
				t.Errorf("%q -> %s missing:\n%s", s, want, text)
			}
		}
		if strings.Contains(s, "ip: [ip]") && !slices.Contains(names,
			"ip") {
			t.Errorf("%q lost ip:\n%s", s, text)
		}
		for _, u := range units {
			if !slices.Contains(c.AllowOpaque, u) {
				t.Errorf("%q -> %s missing from %v:\n%s", s, u,
					c.AllowOpaque, text)
			}
		}
		if strings.Contains(s, "ip.a") && !slices.Contains(c.AllowOpaque,
			"ip.a") {
			t.Errorf("%q lost ip.a:\n%s", s, text)
		}
	}
	if _, ok := LibrariesEdit([]byte("libraries: {ip: [ip]}\n"),
		"x: [x]"); ok {
		t.Error("flow mapping rewritten")
	}
}

// TestDetectDeterministic: the same tree gives the same result
// whatever folder it sits in.
func TestDetectDeterministic(t *testing.T) {
	files := merge(vivado("opt/Xilinx", "2023.2"),
		vivado("opt/Xilinx", "2024.1"), map[string]string{
			"usr/local/diamond/3.13/cae_library/simulation/vhdl/" +
				"b/src/x.vhd": plain,
			"usr/local/diamond/3.13/cae_library/simulation/vhdl/" +
				"a/src/x.vhd": plain,
		})
	a := Detect(tree(t, "linux", nil, files), "")
	b := Detect(tree(t, "linux", nil, files), "")
	if !reflect.DeepEqual(a, b) {
		t.Errorf("results differ:\n%+v\n%+v", a, b)
	}
	if got := fmt.Sprint(Products()); got != "[vivado quartus diamond ghdl]" {
		t.Errorf("Products = %s", got)
	}
}
