-- force_ext_tb: self-checking testbench for force_ext.
library ieee;
use ieee.std_logic_1164.all;

entity force_ext_tb is
end entity force_ext_tb;

architecture tb of force_ext_tb is
  signal a     : std_logic_vector(3 downto 0) := "0000";
  signal b     : std_logic_vector(3 downto 0) := "0000";
  signal op    : std_logic                    := '0';
  signal sel   : std_logic_vector(1 downto 0) := "00";
  signal y     : std_logic_vector(3 downto 0);
  signal bit_o : std_logic;
begin
  -- The instance comes first: an external name may only denote an
  -- object that is already elaborated.
  dut : entity work.force_ext
    port map (
      a     => a,
      b     => b,
      op    => op,
      sel   => sel,
      y     => y,
      bit_o => bit_o
    );

  -- A relative external name, resolved from the testbench architecture.
  -- dut.mid has no testbench counterpart, so the alias is checked against
  -- the values stim produces, sampled at 1 .. 8 ns (delta 0, before stim
  -- applies that instant's force or release). 9 ns is skipped: finish runs
  -- there.
  rel_watch : process is
    alias mid_rel is <<signal dut.mid : std_logic_vector(3 downto 0)>>;
    type mids_t is array (1 to 8) of std_logic_vector(3 downto 0);
    constant C_MID : mids_t := ("1110", "1000", "1100", "1100", "1000",
                                "0110", "0110", "1111");
  begin
    for t in C_MID'range loop
      wait for 1 ns;
      assert mid_rel = C_MID(t) report "relative external name"
        severity error;
    end loop;
    wait;
  end process rel_watch;

  chk : block is
  begin
    stim : process is
      -- Absolute, relative and upward-relative external names.
      alias mid_abs is
        <<signal .force_ext_tb.dut.mid : std_logic_vector(3 downto 0)>>;
      alias mid_up is
        <<signal ^.dut.mid : std_logic_vector(3 downto 0)>>;
      alias magic is <<constant .force_ext_tb.dut.C_MAGIC : integer>>;
    begin
      assert magic = 42 report "external constant" severity error;

      -- process(all): every read signal is in the sensitivity set.
      a <= "1100";
      b <= "1010";
      wait for 1 ns;
      assert y = "1110" report "a or b" severity error;
      assert mid_abs = "1110" and mid_up = "1110"
        report "external signal read" severity error;
      op <= '1';
      wait for 1 ns;
      assert y = "1000" report "a and b after op" severity error;
      b <= "1111";
      wait for 1 ns;
      assert y = "1100" report "a and b after b" severity error;
      assert bit_o = '0' report "bit 0" severity error;
      sel <= "10";
      wait for 1 ns;
      assert bit_o = '1' report "bit 2 after sel" severity error;
      a <= "1000";
      wait for 1 ns;
      assert bit_o = '0' report "bit 2 after mid" severity error;

      -- force overrides the value driven by comb.
      mid_abs <= force "0110";
      wait for 1 ns;
      assert y = "0110" report "forced y" severity error;
      assert bit_o = '1' report "forced bit 2" severity error;
      -- New input values reach the driver, not the forced signal.
      a <= "0001";
      op <= '0';
      wait for 1 ns;
      assert mid_up = "0110" report "force held" severity error;
      assert y = "0110" report "force held at y" severity error;

      -- release returns mid to its driving value (a or b).
      mid_abs <= release;
      wait for 1 ns;
      assert y = "1111" report "released y" severity error;
      assert bit_o = '1' report "released bit 2" severity error;
      b <= "0000";
      wait for 1 ns;
      assert y = "0001" and bit_o = '0' report "driven after release"
        severity error;

      std.env.finish;
      wait;
    end process stim;
  end block chk;
end architecture tb;
