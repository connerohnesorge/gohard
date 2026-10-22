-- The v1.0 fixture check-unsupported-v11/in/env_assert.vhd, now a v1.1
-- simulation: the VHDL assert counter of std.env.
entity env_assert is
end entity env_assert;

architecture sim of env_assert is
begin
  process
  begin
    assert std.env.GetVhdlAssertCount = 0;
    assert false report "probe" severity warning;
    report "count = " & integer'image(std.env.GetVhdlAssertCount);
    assert std.env.GetVhdlAssertCount = 1 report "count" severity error;
    wait;
  end process;
end architecture sim;
