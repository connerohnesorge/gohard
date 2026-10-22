package elab

import (
	"testing"
)

// TestEnvToolAndVersion checks that VHDL_VERSION and TOOL_* give the
// values conditional analysis sees (condanal.Builtin), which the
// design itself compares through `if.
func TestEnvToolAndVersion(t *testing.T) {
	src := "use std.env.all;\n" +
		"entity tb is end entity;\n" +
		"architecture a of tb is\n" +
		"`if TOOL_NAME = \"gohard\" then\n" +
		"  constant c_tool : string := \"gohard\";\n" +
		"`else\n" +
		"  constant c_tool : string := \"?\";\n" +
		"`end if\n" +
		"begin\n" +
		"  process begin\n" +
		"    report vhdl_version & \" \" & tool_type & \" \" &\n" +
		"      tool_vendor & \" \" & tool_name & \" \" & tool_edition &\n" +
		"      \" \" & tool_version;\n" +
		"    assert tool_name = c_tool severity failure;\n" +
		"    wait;\n" +
		"  end process;\n" +
		"end architecture;\n"
	r := gSim(t, []gSource{{"tb.vhd", src}}, "tb",
		EnvOptions{ToolVersion: "1.1.0-test"})
	r.want(t, "2019 SIMULATION gohard gohard community 1.1.0-test")
}

// TestEnvGetenv checks GETENV: only EnvOptions.Vars (exact names), ""
// or null when unset, and the real door only under Real.
func TestEnvGetenv(t *testing.T) {
	src := `use std.env.all;
use std.textio.all;
entity tb is end entity;
architecture a of tb is
begin
  process
    variable l : line;
  begin
    report "[" & getenv("HOME_X") & "][" & getenv("home_x") & "][" &
      getenv("REAL_ONLY") & "]";
    l := getenv("HOME_X");
    report "line=" & l.all;
    l := getenv("UNSET");
    report "null=" & boolean'image(l = null);
    wait;
  end process;
end architecture;
`
	vars := map[string]string{"HOME_X": "/x"}
	getenv := func(k string) (string, bool) {
		if k == "REAL_ONLY" || k == "HOME_X" {
			return "real", true
		}
		return "", false
	}
	r := gSim(t, []gSource{{"tb.vhd", src}}, "tb",
		EnvOptions{Vars: vars, Getenv: getenv})
	r.want(t, "[/x][][]", "line=/x", "null=true")
	r = gSim(t, []gSource{{"tb.vhd", src}}, "tb",
		EnvOptions{Vars: vars, Getenv: getenv, Real: true})
	r.want(t, "[/x][][real]", "line=/x", "null=true")
}

// TestEnvFile checks FILE_NAME, FILE_PATH (project-relative) and
// FILE_LINE (the line of the call) in every return form, in a process,
// in a procedure and in a declaration.
func TestEnvFile(t *testing.T) {
	src := `use std.env.all;
use std.textio.all;
entity tb is end entity;
architecture a of tb is
  procedure where is
    constant here : positive := file_line;
  begin
    report file_name & " " & file_path & " " & file_line &
      " " & integer'image(here);
  end procedure;
begin
  process
    variable l : line;
    variable n : positive;
  begin
    where;
    l := file_path;
    report "path=" & l.all;
    l := file_name;
    report "name=" & l.all;
    n := file_line;
    report "line=" & integer'image(n);
    report "multi " &
      file_line;
    wait;
  end process;
end architecture;
`
	r := gSim(t, []gSource{{"rtl/sub/tb.vhd", src}}, "tb", EnvOptions{})
	r.want(t, "tb.vhd rtl/sub 8 6", "path=rtl/sub", "name=tb.vhd",
		"line=21", "multi 24")
	r = gSim(t, []gSource{{"tb.vhd", src}}, "tb",
		EnvOptions{Real: true, Root: "/proj"})
	r.want(t, "tb.vhd /proj 8 6", "path=/proj", "name=tb.vhd",
		"line=21", "multi 24")
}

// TestEnvCallPath checks GET_CALL_PATH and its TO_STRING forms
// against NVC 1.23's: innermost first, NAME at path/file:line, the
// process as _P0.
func TestEnvCallPath(t *testing.T) {
	src := `use std.env.all;
entity tb is end entity;
architecture a of tb is
  procedure inner is
    variable p : call_path_vector_ptr;
  begin
    p := get_call_path;
    report "len=" & integer'image(p'length) & " " &
      integer'image(p'left);
    report "[" & to_string(p, " | ") & "]";
    report "[" & to_string(p.all, ";") & "]";
    report "[" & to_string(p(0)) & "]";
    report p(1).name.all & " " & p(1).file_name.all & " " &
      p(1).file_path.all & " " & integer'image(p(1).file_line);
    deallocate(p);
  end procedure;
  impure function f return integer is
  begin
    inner;
    return 1;
  end function;
begin
  process
    variable x : integer;
  begin
    inner;
    x := f;
    wait;
  end process;
  lbl : process
    variable cp : call_path_vector_ptr;
  begin
    cp := get_call_path;
    report to_string(cp);
    wait;
  end process;
end architecture;
`
	r := gSim(t, []gSource{{"d/tb.vhd", src}}, "tb", EnvOptions{})
	r.want(t,
		"len=2 0",
		"[d/tb.vhd:7:INNER | d/tb.vhd:26:_P0]",
		"[d/tb.vhd:7:INNER;d/tb.vhd:26:_P0]",
		"[d/tb.vhd:7:INNER]",
		"_P0 tb.vhd d 26",
		"len=3 0",
		"[d/tb.vhd:7:INNER | d/tb.vhd:19:F | d/tb.vhd:27:_P0]",
		"[d/tb.vhd:7:INNER;d/tb.vhd:19:F;d/tb.vhd:27:_P0]",
		"[d/tb.vhd:7:INNER]",
		"F tb.vhd d 19",
		"d/tb.vhd:33:LBL")
}

// timeSrc exercises the date and time functions; the expected
// conversions are NVC 1.23's (run with TZ=UTC).
const timeSrc = `use std.env.all;
entity tb is end entity;
architecture a of tb is
begin
  process
    variable tr : time_record;
    variable r : real;
  begin
    report "now=" & to_string(gmtime) & " local=" & to_string(localtime) &
      " epoch=" & to_string(epoch, 1);
    wait for 1500 ms;
    report "later=" & to_string(gmtime, 3) & " " & to_string(epoch, 1);
    tr := gmtime(0.0);
    report "gm0=" & to_string(tr);
    tr := gmtime(946684800.5);
    report "gm=" & to_string(tr) & " " & to_string(tr, 3) & " " &
      to_string(tr, 6);
    report "wd=" & dayofweek'image(tr.weekday) & " doy=" &
      integer'image(tr.dayofyear) & " mo=" & integer'image(tr.month) &
      " us=" & integer'image(tr.microsecond);
    report "ep=" & to_string(epoch(tr), 1);
    tr := tr + 86400.25;
    report "plus=" & to_string(tr, 6);
    tr := 3600.0 + tr;
    report "plus2=" & to_string(tr, 6);
    tr := tr - 7200.0;
    report "minus=" & to_string(tr, 6);
    r := tr - gmtime(946684800.0);
    report "diff=" & to_string(r, 2);
    report "lt=" & to_string(localtime(gmtime(1.0e9))) & " " &
      to_string(localtime(1.0e9)) & " " & to_string(gmtime(gmtime(1.0e9)));
    report "leap=" & to_string(gmtime(951782400.0)) & " doy=" &
      integer'image(gmtime(951782400.0).dayofyear);
    report "t2s=" & to_string(time_to_seconds(1500 ms), 1) & " s2t=" &
      time'image(seconds_to_time(2.5));
    wait;
  end process;
end architecture;
`

// TestEnvTime checks the deterministic clock (the epoch plus the
// simulated time, UTC; LOCALTIME = GMTIME) and the conversions.
func TestEnvTime(t *testing.T) {
	conv := []string{
		"gm0=1970-01-01T00:00:00",
		"gm=2000-01-01T00:00:00 2000-01-01T00:00:00.500 " +
			"2000-01-01T00:00:00.500000",
		"wd=saturday doy=0 mo=0 us=500000",
		"ep=946684800.5",
		"plus=2000-01-02T00:00:00.750000",
		"plus2=2000-01-02T01:00:00.750000",
		"minus=2000-01-01T23:00:00.750000",
		"diff=82800.75",
		"lt=2001-09-09T01:46:40 2001-09-09T01:46:40 2001-09-09T01:46:40",
		"leap=2000-02-29T00:00:00 doy=59",
		"t2s=1.5 s2t=2500000000000000 fs",
	}
	r := gSim(t, []gSource{{"tb.vhd", timeSrc}}, "tb", EnvOptions{})
	r.want(t, append([]string{
		"now=2000-01-01T00:00:00 local=2000-01-01T00:00:00 " +
			"epoch=946684800.0",
		"later=2000-01-01T00:00:01.500 946684801.5"}, conv...)...)
	r = gSim(t, []gSource{{"tb.vhd", timeSrc}}, "tb",
		EnvOptions{Epoch: gEpoch})
	r.want(t, append([]string{
		"now=2001-09-09T01:46:40 local=2001-09-09T01:46:40 " +
			"epoch=1000000000.0",
		"later=2001-09-09T01:46:41.500 1000000001.5"}, conv...)...)
}

// TestEnvAsserts checks the assert counter-and-format API: only
// enabled assertion violations count (not reports), the total is of
// warning and above (as in NVC), and a format changes the diagnostic
// only.
func TestEnvAsserts(t *testing.T) {
	src := `use std.env.all;
entity tb is end entity;
architecture a of tb is
begin
  process
    variable ok : boolean;
  begin
    report "c0=" & integer'image(GetVhdlAssertCount) & " f=" &
      boolean'image(IsVhdlAssertFailed);
    assert false report "n1" severity note;
    assert false report "w1" severity warning;
    assert false report "e1" severity error;
    report "just a report" severity warning;
    report "c=" & integer'image(GetVhdlAssertCount) & " n=" &
      integer'image(GetVhdlAssertCount(note)) & " w=" &
      integer'image(GetVhdlAssertCount(warning)) & " e=" &
      integer'image(GetVhdlAssertCount(error)) & " f=" &
      boolean'image(IsVhdlAssertFailed) & " fw=" &
      boolean'image(IsVhdlAssertFailed(warning)) & " ff=" &
      boolean'image(IsVhdlAssertFailed(failure));
    SetVhdlAssertEnable(warning, false);
    report "en w=" & boolean'image(GetVhdlAssertEnable(warning)) &
      " e=" & boolean'image(GetVhdlAssertEnable(error)) & " dflt=" &
      boolean'image(GetVhdlAssertEnable);
    assert false report "w2 hidden" severity warning;
    report "c2=" & integer'image(GetVhdlAssertCount(warning));
    SetVhdlAssertEnable(true);
    assert false report "w3" severity warning;
    ClearVhdlAssert;
    report "c3=" & integer'image(GetVhdlAssertCount);
    report "fmt=[" & GetVhdlAssertFormat(error) & "]";
    report "rs=" & severity_level'image(GetVhdlReadSeverity);
    SetVhdlReadSeverity(warning);
    report "rs=" & severity_level'image(GetVhdlReadSeverity);
    SetVhdlReadSeverity;
    report "rs=" & severity_level'image(GetVhdlReadSeverity);
    SetVhdlAssertFormat(note, "{S}: {r}", ok);
    report "ok=" & boolean'image(ok) & " fmt=" &
      GetVhdlAssertFormat(note);
    SetVhdlAssertFormat(note, "{zz", ok);
    report "ok=" & boolean'image(ok) & " fmt=" &
      GetVhdlAssertFormat(note);
    SetVhdlAssertFormat(note, "");
    wait;
  end process;
end architecture;
`
	r := gSim(t, []gSource{{"tb.vhd", src}}, "tb", EnvOptions{})
	r.errs = nil // the error assertion e1
	r.want(t, "c0=0 f=false", "n1", "w1", "e1", "just a report",
		"c=2 n=1 w=1 e=1 f=true fw=true ff=false",
		"en w=false e=true dflt=true", "c2=1", "w3", "c3=0", "fmt=[]",
		"rs=error", "rs=warning", "rs=failure",
		"ok=true fmt={S}: {r}", "ok=false fmt={S}: {r}")
	var formatted bool
	for _, d := range r.k.Diagnostics() {
		if d.Message == "report note: NOTE: ok=true fmt={S}: {r}" ||
			d.Message == "report: NOTE: ok=true fmt={S}: {r}" {
			formatted = true
		}
	}
	if !formatted {
		for _, d := range r.k.Diagnostics() {
			t.Log(d.Message)
		}
		t.Error("the note format did not reach the diagnostic")
	}
}
