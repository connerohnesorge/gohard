-- hier_generate_tb: self-checking generate-hierarchy testbench (#8).
-- Uses the defaults N = 4, BASE = 2: lanes of 2, 3, 4 and 5 bits packed
-- into a 14-bit bus. Ends with std.env.finish.
library ieee;
use ieee.std_logic_1164.all;

entity hier_generate_tb is
end entity hier_generate_tb;

architecture sim of hier_generate_tb is
  constant N    : positive := 4;
  constant BASE : positive := 2;
  constant TOTAL : positive := N * BASE + N * (N - 1) / 2;
  type vec_array is array (natural range <>)
    of std_logic_vector(TOTAL - 1 downto 0);
  -- The last vector has odd parity, so the multi branch (xor din = '1')
  -- and the single branch (parity <= '0') give different values.
  constant stimuli : vec_array := (
    "00000000000000", "11111111111111", "10110011100101",
    "01001100011010", "00000000000001");
  signal din    : std_logic_vector(TOTAL - 1 downto 0) := (others => '0');
  signal dout   : std_logic_vector(TOTAL - 1 downto 0);
  signal parity : std_logic;

  function expect (v : std_logic_vector(TOTAL - 1 downto 0))
    return std_logic_vector is
    variable r : std_logic_vector(TOTAL - 1 downto 0) := v;
    variable lo : natural;
  begin
    for i in 0 to N - 1 loop
      lo := i * BASE + i * (i - 1) / 2;
      if i mod 2 = 1 then
        r(lo + BASE + i - 1 downto lo) := not v(lo + BASE + i - 1 downto lo);
      end if;
    end loop;
    return r;
  end function expect;
begin
  dut : entity work.hier_generate
    generic map (N => N, BASE => BASE)
    port map (din => din, dout => dout, parity => parity);

  check : process
  begin
    for k in stimuli'range loop
      din <= stimuli(k);
      wait for 10 ns;
      assert dout = expect(stimuli(k))
        report "lane output mismatch" severity failure;
      assert parity = xor stimuli(k)
        report "parity mismatch" severity failure;
    end loop;
    std.env.finish;
    wait;
  end process check;
end architecture sim;
