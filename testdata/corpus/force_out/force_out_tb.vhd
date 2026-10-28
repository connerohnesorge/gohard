-- force_out_tb: self-checking testbench for force_out (#63). It forces
-- the out port dut.y through an external name, once with the explicit
-- force mode out and once with the default mode, which for a port of
-- mode out is out too (LRM 10.5.2.1). Forcing the driving value of the
-- port overrides the inverter at the actual y and at the port itself;
-- release out returns both to the inverter's value. Ends with
-- std.env.finish.
library ieee;
use ieee.std_logic_1164.all;

entity force_out_tb is
end entity force_out_tb;

architecture tb of force_out_tb is
  signal a      : std_logic := '0';
  signal y      : std_logic;
  signal y_seen : std_logic;
begin
  -- The instance comes first: an external name may only denote an
  -- object that is already elaborated.
  dut : entity work.force_out
    port map (a => a, y => y, y_seen => y_seen);

  stim : process is
    alias y_port is <<signal .force_out_tb.dut.y : std_logic>>;
  begin
    wait for 1 ns;
    assert y = '1' and y_seen = '1' report "y = not a" severity error;

    -- 1 ns: force out sets the driving value of the port.
    y_port <= force out '0';
    wait for 1 ns;
    assert y = '0' report "force out at the actual" severity error;
    assert y_seen = '0' report "force out at the port" severity error;
    -- 2 ns: the inverter's new value is hidden by the force.
    a <= '1';
    wait for 1 ns;
    a <= '0';
    wait for 1 ns;
    assert y = '0' and y_seen = '0' report "force out held"
      severity error;

    -- 4 ns: release out returns the port to the inverter's value.
    y_port <= release out;
    wait for 1 ns;
    assert y = '1' and y_seen = '1' report "released out"
      severity error;

    -- 5 ns: the default force mode of an out port is out.
    y_port <= force 'Z';
    wait for 1 ns;
    assert y = 'Z' and y_seen = 'Z' report "default force mode"
      severity error;
    a <= '1';
    wait for 1 ns;
    assert y = 'Z' report "default force held" severity error;
    y_port <= release;
    wait for 1 ns;
    assert y = '0' and y_seen = '0' report "released" severity error;

    std.env.finish;
    wait;
  end process stim;
end architecture tb;
