-- standin_model_tb: self-checking against the stand-in's documented
-- behaviour (reset clears dout and valid; two clocks of latency through
-- the rising-edge register; dout = din xor x"5A"). Stops with
-- std.env.finish.
library ieee;
use ieee.std_logic_1164.all;
use std.env.all;

entity standin_model_tb is
end entity standin_model_tb;

architecture sim of standin_model_tb is
  type bytes_t is array (natural range <>) of std_logic_vector(7 downto 0);
  -- Consecutive values differ, so a one-clock path is caught.
  constant C_IN : bytes_t := (x"00", x"5A", x"A5", x"FF", x"3C");

  signal clk   : std_logic := '0';
  signal rst   : std_logic := '1';
  signal din   : std_logic_vector(7 downto 0) := (others => '0');
  signal dout  : std_logic_vector(7 downto 0);
  signal valid : std_logic;
  signal done  : boolean := false;
begin
  clk <= not clk after 5 ns when not done;

  dut : entity work.standin_model
    port map (clk => clk, rst => rst, din => din, dout => dout,
              valid => valid);

  -- The wrapper registers on the rising edge only: 2 ns after every
  -- falling edge, dout and valid must not have changed since the rising
  -- edge before it. A path that also follows clk's falling edge (the
  -- falling-edge samples alone cannot see it) fails here.
  edge_mon : process
  begin
    wait until falling_edge(clk);
    wait for 2 ns;
    assert dout'last_event > 2 ns and valid'last_event > 2 ns
      report "output changed off the rising edge" severity failure;
  end process edge_mon;

  -- Clock rises at 5, 15, 25, ... and falls at 10, 20, 30, ... ns.
  -- rst is held over the rising edges at 5 and 15 ns, so at 20 ns both
  -- pipeline stages hold the stand-in's reset outputs. A new din goes in
  -- on every falling edge from 20 ns; the value sent at fall t must reach
  -- dout at fall t + 20 ns (two clocks), not earlier.
  stim : process
  begin
    wait until falling_edge(clk);
    wait until falling_edge(clk);
    assert dout = x"00" report "reset: dout /= x00" severity failure;
    assert valid = '0' report "reset: valid /= 0" severity failure;
    rst <= '0';
    for j in 0 to C_IN'length loop
      if j <= C_IN'high then
        din <= C_IN(j);
      end if;
      wait until falling_edge(clk);
      if j = 0 then
        -- One clock after reset release: still the reset outputs.
        assert valid = '0'
          report "latency: valid set after one clock" severity failure;
        assert dout = x"00"
          report "latency: dout changed after one clock" severity failure;
      else
        assert valid = '1' report "valid not set" severity failure;
        assert dout = (C_IN(j - 1) xor x"5A")
          report "dout /= din xor x5A two clocks earlier"
          severity failure;
      end if;
    end loop;
    done <= true;
    finish;
  end process stim;
end architecture sim;
