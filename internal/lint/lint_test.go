package lint

import (
	"fmt"
	"maps"
	"slices"
	"strings"
	"testing"

	"github.com/connerohnesorge/gohard/internal/config"
	"github.com/connerohnesorge/gohard/internal/diag"
	"github.com/connerohnesorge/gohard/internal/sema"
)

// allOn is every rule on as a warning.
var allOn = config.Lint{UnusedSignal: "warning", SensitivityList: "warning",
	Latch: "warning", ClockEdgeStyle: "warning"}

// analyze analyzes files (path to text) in path order and fails on any
// analysis diagnostic: lint findings must come from legal VHDL.
func analyze(t *testing.T, files map[string]string) *sema.Result {
	t.Helper()
	var srcs []sema.Source
	for _, p := range slices.Sorted(maps.Keys(files)) {
		srcs = append(srcs, sema.Source{Path: p, Text: []byte(files[p])})
	}
	r := sema.Analyze(sema.Input{Files: srcs, Config: config.Defaults()})
	for _, d := range r.Diagnostics {
		t.Fatalf("analysis: %s", show(d))
	}
	return r
}

func show(d diag.Diagnostic) string {
	return fmt.Sprintf("%s:%d:%d: %s: %s", d.Path, d.Line, d.Col, d.Code,
		d.Message)
}

func shows(ds []diag.Diagnostic) []string {
	var out []string
	for _, d := range ds {
		out = append(out, show(d))
	}
	slices.Sort(out)
	return out
}

// lintCase is one design (a.vhd) and the findings every rule gives on
// it, as "a.vhd:LINE:COL: CODE: MESSAGE".
type lintCase struct {
	name string
	src  string
	want []string
}

func runCases(t *testing.T, cases []lintCase) {
	t.Helper()
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := analyze(t, map[string]string{"a.vhd": c.src})
			got := shows(Check(r, allOn))
			want := slices.Clone(c.want)
			slices.Sort(want)
			if !slices.Equal(got, want) {
				t.Errorf("got\n\t%s\nwant\n\t%s",
					strings.Join(got, "\n\t"), strings.Join(want, "\n\t"))
			}
		})
	}
}

const header = "library ieee; use ieee.std_logic_1164.all;\n"

// TestPortModes: lint/unused-signal on ports of every mode.
func TestPortModes(t *testing.T) {
	runCases(t, []lintCase{{
		name: "each mode used",
		src: header + `entity e is
  port (i : in std_logic; o : out std_logic; io : inout std_logic;
        b : buffer std_logic; l : linkage std_logic; d : std_logic);
end;
architecture a of e is
begin
  o <= i and d;
  b <= io;
end;
`,
	}, {
		name: "each mode unused",
		src: header + `entity e is
  port (i : in std_logic; o : out std_logic; io : inout std_logic;
        b : buffer std_logic; l : linkage std_logic; d : std_logic);
end;
architecture a of e is
begin
end;
`,
		want: []string{
			"a.vhd:3:9: lint/unused-signal: port i is never read",
			"a.vhd:3:27: lint/unused-signal: port o is never assigned",
			"a.vhd:3:46: lint/unused-signal: port io is never used",
			"a.vhd:4:9: lint/unused-signal: port b is never used",
			// d has the default mode in; linkage ports are never
			// reported.
			"a.vhd:4:54: lint/unused-signal: port d is never read",
		},
	}, {
		// An inout or buffer port that is only read or only written
		// is used.
		name: "inout and buffer one way",
		src: header + `entity e is
  port (io1, io2 : inout std_logic; b1, b2 : buffer std_logic;
        o : out std_logic);
end;
architecture a of e is
begin
  o <= io1 and b1;
  io2 <= '0';
  b2 <= '1';
end;
`,
	}, {
		// A port read in the entity's own statement part, or in only
		// one of two architectures, is read; an entity without an
		// architecture has nothing that could read its ports.
		name: "entity statements and architectures",
		src: header + `entity e is
  port (i, j : in std_logic; o : out std_logic);
begin
  assert i = '0' or i = '1';
end;
architecture a1 of e is
begin
  o <= j;
end;
architecture a2 of e is
begin
  o <= '0';
end;
` + header + `entity bare is
  port (x : in std_logic; y : out std_logic);
end;
`,
	}, {
		// Ports connected in a port map count as read and written,
		// whatever the formal's mode.
		name: "port map",
		src: header + `entity sub is port (x : in std_logic; y : out std_logic); end;
architecture a of sub is begin y <= x; end;
` + header + `entity e is port (i : in std_logic; o : out std_logic); end;
architecture a of e is
begin
  u : entity work.sub port map (x => i, y => o);
end;
`,
	}})
}

// TestGenerates: signals declared in generates, and processes inside
// them, are checked like any other.
func TestGenerates(t *testing.T) {
	runCases(t, []lintCase{{
		name: "for and if generate",
		src: header + `entity e is
  generic (N : positive := 4; USE_B : boolean := true);
  port (a, b, en : in std_logic; y : out std_logic_vector(N - 1 downto 0);
        z : out std_logic);
end;
architecture a of e is
begin
  g : for k in 0 to N - 1 generate
    signal s, dead : std_logic;
  begin
    s <= a xor b;
    p : process (s, en) begin
      if en = '1' then
        y(k) <= s;
      end if;
    end process;
  end generate;
  gb : if USE_B generate
    signal t : std_logic;
  begin
    t <= b;
    q : process (en) begin
      z <= t and en;
    end process;
  end generate;
  gn : if not USE_B generate
  begin
    z <= a;
  end generate;
end;
`,
		want: []string{
			"a.vhd:10:15: lint/unused-signal: signal dead is never used",
			"a.vhd:15:9: lint/latch: y is not assigned on every path " +
				"through the process, so it infers a latch",
			"a.vhd:24:12: lint/sensitivity-list: t is read in the " +
				"process but is not in its sensitivity list",
		},
	}, {
		// A generate parameter and a generic are constants: reading
		// them needs no sensitivity, and an if on a generic picks the
		// same path every time, so it infers no latch.
		name: "static conditions",
		src: header + `entity e is
  generic (MODE : natural := 0; FAST : boolean := false);
  port (a, b : in std_logic; y, z : out std_logic_vector(1 downto 0));
end;
architecture a of e is
begin
  g : for k in 0 to 1 generate
    p : process (a, b) begin
      if FAST then
        y(k) <= a;
      elsif k = 1 then
        y(k) <= b;
      end if;
      case MODE is
        when 0 => z(k) <= a;
        when others => null;
      end case;
    end process;
  end generate;
end;
`,
	}})
}

// TestRecords: an element of a record is the record for every rule.
func TestRecords(t *testing.T) {
	runCases(t, []lintCase{{
		name: "fields",
		src: header + `entity e is
  port (a, b, en : in std_logic; y : out std_logic);
end;
architecture a of e is
  type pair is record x, w : std_logic; end record;
  signal r, q, unread : pair;
begin
  -- r.w is written only under en: the whole record counts as
  -- assigned on every path once r.x is, so no latch.
  p1 : process (a, b, en) begin
    r.x <= a;
    if en = '1' then
      r.w <= b;
    end if;
  end process;
  -- Listing q covers reads of its fields; reading a field of r that
  -- the list does not name is a finding for r.
  p2 : process (q) begin
    y <= q.x or r.w;
  end process;
  q <= (x => a, w => b);
  unread.x <= a;
end;
`,
		want: []string{
			"a.vhd:20:17: lint/sensitivity-list: r is read in the " +
				"process but is not in its sensitivity list",
			"a.vhd:7:16: lint/unused-signal: signal unread is never read",
		},
	}, {
		// A name read in the index of a target is a read, not a write.
		name: "index of a target",
		src: header + `entity e is port (a : in std_logic; y : out std_logic); end;
architecture a of e is
  type rec is record i : natural range 0 to 1; end record;
  signal sel : rec;
  signal v : std_logic_vector(0 to 1);
begin
  v(sel.i) <= a;
  y <= v(0);
end;
`,
		want: []string{
			"a.vhd:5:10: lint/unused-signal: signal sel is never assigned",
		},
	}})
}

// TestAliases: a read or write through an alias is one of its object,
// and an alias declaration alone uses nothing.
func TestAliases(t *testing.T) {
	runCases(t, []lintCase{{
		name: "unused-signal",
		src: header + `entity e is
  port (i : in std_logic; o : out std_logic; o2 : out std_logic);
end;
architecture a of e is
  type pair is record x, w : std_logic; end record;
  signal s, never, only_aliased : std_logic;
  signal r : pair;
  alias as_s is s;
  alias as_i is i;
  alias as_o is o;
  alias as_rx is r.x;
  alias as_never is only_aliased;
begin
  as_s <= as_i;
  as_o <= s;
  as_rx <= s;
  o2 <= r.x;
end;
`,
		want: []string{
			"a.vhd:7:13: lint/unused-signal: signal never is never used",
			"a.vhd:7:20: lint/unused-signal: signal only_aliased is " +
				"never used",
		},
	}, {
		name: "latch and sensitivity",
		src: header + `entity e is
  port (a, b, en : in std_logic; y, z : out std_logic);
end;
architecture a of e is
  alias as_a is a;
  alias as_b is b;
  alias as_y is y;
begin
  -- Writing y through as_y on one path is a latch of y; reading a
  -- through as_a, unlisted, names as_a.
  p1 : process (en) begin
    if en = '1' then
      as_y <= as_a;
    end if;
  end process;
  -- The alias in the list covers reads of b.
  p2 : process (as_b) begin
    z <= b;
  end process;
end;
`,
		want: []string{
			"a.vhd:14:7: lint/latch: y is not assigned on every path " +
				"through the process, so it infers a latch",
			"a.vhd:14:15: lint/sensitivity-list: as_a is read in the " +
				"process but is not in its sensitivity list",
		},
	}})
}

// TestProcedureCalls: a call assigns only what it passes to an out or
// inout signal parameter.
func TestProcedureCalls(t *testing.T) {
	runCases(t, []lintCase{{
		name: "modes",
		src: header + `entity e is
  port (a, en : in std_logic; y, z : out std_logic);
end;
architecture a of e is
  procedure drive (signal i : in std_logic; signal o : out std_logic;
                   c : in std_logic) is
  begin
    o <= i and c;
  end procedure;
begin
  -- y is driven on one path; a and en are only read by the call.
  p : process (a, en) begin
    z <= '0';
    if en = '1' then
      drive(a, y, en);
    end if;
  end process;
end;
`,
		want: []string{
			"a.vhd:16:16: lint/latch: y is not assigned on every path " +
				"through the process, so it infers a latch",
		},
	}})
}

// TestClockEdgeStyle: 'event in a process is reported only in a design
// that also uses rising_edge or falling_edge, and never in a function.
func TestClockEdgeStyle(t *testing.T) {
	runCases(t, []lintCase{{
		name: "one style",
		src: header + `entity e is port (clk, d : in std_logic; q : out std_logic); end;
architecture a of e is
  function changed (signal s : std_logic) return boolean is
  begin
    return s'event;
  end function;
begin
  p : process (clk) begin
    if clk'event and clk = '1' then
      q <= d;
    end if;
  end process;
end;
`,
	}, {
		// 'event in an if or wait condition is an edge test; in an
		// assertion it is not.
		name: "mixed",
		src: header + `entity e is
  port (clk, d : in std_logic; q, r, w : out std_logic);
end;
architecture a of e is
begin
  p : process (clk) begin
    if clk'event and clk = '1' then
      q <= d;
    end if;
  end process;
  p2 : process (clk) begin
    if falling_edge(clk) then
      r <= d;
    end if;
  end process;
  p3 : process begin
    wait until clk'event and clk = '1';
    assert not d'event report "d changed on the edge";
    w <= d;
  end process;
end;
`,
		want: []string{
			"a.vhd:8:12: lint/clock-edge-style: this edge test uses " +
				"'event; the design also uses rising_edge or falling_edge",
			"a.vhd:18:20: lint/clock-edge-style: this edge test uses " +
				"'event; the design also uses rising_edge or falling_edge",
		},
	}})
}
