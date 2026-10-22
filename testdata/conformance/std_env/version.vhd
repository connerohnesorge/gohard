-- VHDL_VERSION and the TOOL_* functions give the values of the
-- conditional-analysis built-in identifiers (#18, #31).
use std.env.all;

entity version_tb is
end entity version_tb;

architecture sim of version_tb is
`if TOOL_NAME = "gohard" then
  constant C_NAME    : string := "gohard";
  constant C_VENDOR  : string := "gohard";
  constant C_EDITION : string := "community";
`elsif TOOL_NAME = "nvc" then
  constant C_NAME    : string := "nvc";
  constant C_VENDOR  : string := "https://www.nickg.me.uk/nvc";
  constant C_EDITION : string := "GPL";
`else
  constant C_NAME    : string := "?";
  constant C_VENDOR  : string := "?";
  constant C_EDITION : string := "?";
`end if
`if VHDL_VERSION = "2019" then
  constant C_VERSION : string := "2019";
`else
  constant C_VERSION : string := "?";
`end if
begin
  check : process
  begin
    report "VHDL_VERSION = " & VHDL_VERSION;
    report "TOOL_TYPE = " & TOOL_TYPE;
    assert VHDL_VERSION = C_VERSION report "VHDL_VERSION" severity failure;
    assert TOOL_NAME = C_NAME report "TOOL_NAME" severity failure;
    assert TOOL_VENDOR = C_VENDOR report "TOOL_VENDOR" severity failure;
    assert TOOL_EDITION = C_EDITION report "TOOL_EDITION" severity failure;
    assert TOOL_VERSION'length > 0 report "TOOL_VERSION" severity failure;
    report "TOOL_* agree with conditional analysis";
    wait;
  end process check;
end architecture sim;
