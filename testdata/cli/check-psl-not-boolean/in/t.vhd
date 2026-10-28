library ieee; use ieee.std_logic_1164.all;
entity t is end;
architecture rtl of t is
  signal clk, a : std_logic := '0';
  signal i : integer := 0;
  signal v : std_logic_vector(3 downto 0) := "0000";
  default clock is rising_edge(clk);
begin
  clk <= not clk after 5 ns;
  a <= '1' after 20 ns;
  i <= 1 after 20 ns;
  v <= "0001" after 20 ns;
  x1: assert always prev(i);
  x2: assert always prev(v);
  x3: assert always (prev(i));
  x4: assert always {a; prev(i)};
  x5: assert always prev(a);
  x6: assert always (prev(i) = 1 -> a);
  x7: assert always ((a -> a) abort prev(i));
  x8: assert always ((a -> a) async_abort prev(i));
  x9: assert always ((a -> a) sync_abort prev(v));
  x10: assert always (a -> a) abort i;
  x11: assert always (a -> a) abort prev(a);
end;
