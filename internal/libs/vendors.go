package libs

// product is one vendor tool whose install holds library sources.
type product struct {
	id, name string
	// bases are the usual install folders per OS ("~/" is the home
	// folder); linux is used for any OS without an entry.
	bases map[string][]string
	// layouts are the install folders under a base: slash paths whose
	// "*" element is the version.
	layouts []string
	// envs are variables naming a folder up levels below the install
	// folder; progs are programs on PATH whose folder is up levels
	// below it.
	envs, progs []locator
	// marker is a folder under the install folder that proves it is
	// one.
	marker string
	libs   []libRule
}

type locator struct {
	name string
	up   int
}

// libRule is where one library (or, with each, one library per
// subfolder) lives under the install folder.
type libRule struct {
	name string // library name; "" with each
	rel  string // folder under the install folder
	each bool   // every subfolder of rel is a library named like it
	sub  string // with each: the source folder under each subfolder
}

// products are the vendor tools `gohard libs detect` searches, in
// output order (#1).
var products = []product{
	{
		id: "vivado", name: "Xilinx Vivado",
		bases: map[string][]string{
			"linux": {"/opt/Xilinx", "/tools/Xilinx", "~/Xilinx"},
			"darwin": {"/Applications/Xilinx", "/opt/Xilinx",
				"~/Xilinx"},
			"windows": {"C:/Xilinx", "C:/AMD"},
		},
		layouts: []string{"Vivado/*", "*/Vivado"},
		envs:    []locator{{"XILINX_VIVADO", 0}},
		progs:   []locator{{"vivado", 1}},
		marker:  "data/vhdl/src",
		libs: []libRule{
			{name: "unisim", rel: "data/vhdl/src/unisims"},
			{name: "unimacro", rel: "data/vhdl/src/unimacro"},
			{name: "secureip", rel: "data/secureip"},
		},
	},
	{
		id: "quartus", name: "Intel Quartus",
		bases: map[string][]string{
			"linux": {"/opt/intelFPGA", "/opt/intelFPGA_lite",
				"/opt/intelFPGA_pro", "/opt/altera",
				"~/intelFPGA", "~/intelFPGA_lite", "~/intelFPGA_pro",
				"~/altera"},
			"darwin": {"/Applications/intelFPGA",
				"/Applications/intelFPGA_lite", "~/intelFPGA",
				"~/intelFPGA_lite"},
			"windows": {"C:/intelFPGA", "C:/intelFPGA_lite",
				"C:/intelFPGA_pro", "C:/altera", "C:/altera_lite"},
		},
		layouts: []string{"*/quartus"},
		envs:    []locator{{"QUARTUS_ROOTDIR", 0}},
		progs:   []locator{{"quartus_sh", 1}},
		marker:  "eda/sim_lib",
		libs: []libRule{
			{name: "altera_mf", rel: "eda/sim_lib"},
			{rel: "../modelsim_ase/altera/vhdl", each: true},
			{rel: "../questa_fse/intel/vhdl", each: true},
		},
	},
	{
		id: "diamond", name: "Lattice Diamond",
		bases: map[string][]string{
			"linux": {"/usr/local/diamond", "/opt/lscc/diamond",
				"~/lscc/diamond"},
			"darwin": {"/Applications/lscc/diamond",
				"/opt/lscc/diamond", "~/lscc/diamond"},
			"windows": {"C:/lscc/diamond"},
		},
		layouts: []string{"*"},
		envs:    []locator{{"FOUNDRY", 1}},
		progs:   []locator{{"diamondc", 2}, {"pnmainc", 2}},
		marker:  "cae_library",
		libs: []libRule{
			{rel: "cae_library/simulation/vhdl", each: true,
				sub: "src"},
			{rel: "active-hdl/vlib", each: true},
		},
	},
}

// ghdlBases are the usual GHDL library folders (lib/ghdl), whose
// vendors/ subfolder holds the libraries GHDL's vendor scripts
// compile.
var ghdlBases = map[string][]string{
	"linux": {"/usr/lib/ghdl", "/usr/local/lib/ghdl",
		"/opt/ghdl/lib/ghdl", "~/.local/lib/ghdl"},
	"darwin": {"/opt/homebrew/lib/ghdl", "/usr/local/lib/ghdl",
		"/opt/ghdl/lib/ghdl"},
	"windows": {"C:/msys64/mingw64/lib/ghdl", "C:/msys64/ucrt64/lib/ghdl",
		"C:/Program Files/GHDL/lib/ghdl", "C:/GHDL/lib/ghdl"},
}

// Products returns the ids of the vendor tools searched, in output
// order, for --version and the documentation.
func Products() []string {
	out := make([]string, 0, len(products)+1)
	for _, p := range products {
		out = append(out, p.id)
	}
	return append(out, "ghdl")
}
