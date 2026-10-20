package main

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// Project is one #19 benchmark project, built from the corpus under
// testdata into a scratch folder.
type Project struct {
	Name string
	// Copies are (testdata source, destination) folder pairs; only
	// VHDL files are copied, without the paths in Skip.
	Copies [][2]string
	Skip   []string
	// Config is the gohard.yaml written at the root ("" for none).
	Config string
	// Target is the edited document (slash path from the root).
	Target string
	// Testbench is run for the first-simulation-progress measure
	// ("" for none) and TestbenchFile is its file.
	Testbench, TestbenchFile string
	// SimOnly projects are measured for simulation progress only.
	SimOnly bool
}

// Projects are the small, medium and large projects of #19.
var Projects = []Project{
	{
		Name:      "small",
		Copies:    [][2]string{{"testdata/corpus/overload_ops", "."}},
		Target:    "overload_ops.vhd",
		Testbench: "overload_ops_tb", TestbenchFile: "overload_ops_tb.vhd",
	},
	{
		Name:   "medium",
		Copies: [][2]string{{"testdata/open-logic/src", "src"}},
		Config: "libraries:\n  olo: [src]\n",
		Target: "src/base/vhdl/olo_base_fifo_sync.vhd",
	},
	{
		Name: "large",
		Copies: [][2]string{
			{"testdata/neorv32/rtl", "rtl"},
			{"testdata/neorv32/sim", "sim"},
			{"testdata/libs/osvvm", "osvvm"},
		},
		// OSVVM's deprecated copies redeclare units; the template has
		// build-script placeholders (osvvm.pro compiles neither).
		Skip: []string{"rtl/verilog", "osvvm/deprecated",
			"osvvm/OsvvmSettingsPkg_template.vhd"},
		Config:    "libraries:\n  neorv32: [rtl, sim]\n  osvvm: [osvvm]\n",
		Target:    "rtl/core/neorv32_cpu_control.vhd",
		Testbench: "neorv32_tb", TestbenchFile: "sim/neorv32_tb.vhd",
	},
	{
		// A testbench that runs until the step budget stops it, so
		// the kernel reports progress (the large project's testbench
		// does not run while the project has errors).
		Name:      "sim",
		Copies:    [][2]string{{"testdata/corpus/budget_hang", "."}},
		Target:    "budget_hang.vhd",
		Testbench: "budget_hang_tb", TestbenchFile: "budget_hang_tb.vhd",
		SimOnly: true,
	},
}

func projectByName(name string) (Project, bool) {
	for _, p := range Projects {
		if p.Name == name {
			return p, true
		}
	}
	return Project{}, false
}

// Build writes the project under dst from the repository at repo and
// returns the number of VHDL files.
func (p Project) Build(repo, dst string) (int, error) {
	n := 0
	for _, cp := range p.Copies {
		src := filepath.Join(repo, filepath.FromSlash(cp[0]))
		err := filepath.WalkDir(src, func(path string, d fs.DirEntry,
			err error,
		) error {
			if err != nil {
				return err
			}
			rel, _ := filepath.Rel(src, path)
			out := filepath.ToSlash(filepath.Join(cp[1], rel))
			for _, s := range p.Skip {
				if out == s || strings.HasPrefix(out, s+"/") {
					if d.IsDir() {
						return filepath.SkipDir
					}
					return nil
				}
			}
			ext := strings.ToLower(filepath.Ext(path))
			if d.IsDir() || (ext != ".vhd" && ext != ".vhdl") {
				return nil
			}
			b, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			to := filepath.Join(dst, filepath.FromSlash(out))
			if err := os.MkdirAll(filepath.Dir(to), 0o755); err != nil {
				return err
			}
			n++
			return os.WriteFile(to, b, 0o644)
		})
		if err != nil {
			return 0, err
		}
	}
	if p.Config != "" {
		err := os.WriteFile(filepath.Join(dst, "gohard.yaml"),
			[]byte(p.Config), 0o644)
		if err != nil {
			return 0, err
		}
	}
	if _, err := os.Stat(filepath.Join(dst,
		filepath.FromSlash(p.Target))); err != nil {
		return 0, fmt.Errorf("%s: target: %w", p.Name, err)
	}
	return n, nil
}
