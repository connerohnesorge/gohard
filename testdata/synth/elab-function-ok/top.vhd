library ieee;
use ieee.std_logic_1164.all;
use ieee.numeric_std.all;

entity top is
  generic (
    CLK_PERIOD : time     := 10 ns;
    DEPTH      : positive := 100
  );
  port (
    clk : in  std_ulogic;
    rst : in  std_ulogic;
    q   : out std_ulogic
  );
end entity;

architecture rtl of top is
  -- time and real variables appear only inside functions that are
  -- called for constants, so they are folded while elaborating.
  function cycles (span, period : time) return natural is
    variable t : time    := 0 ns;
    variable n : natural := 0;
  begin
    while t < span loop
      t := t + period;
      n := n + 1;
    end loop;
    return n;
  end function;

  function clog2 (v : positive) return natural is
    variable r : real    := 1.0;
    variable p : natural := 0;
  begin
    while r < real(v) loop
      r := r * 2.0;
      p := p + 1;
    end loop;
    return p;
  end function;

  constant N : natural := cycles(1 us, CLK_PERIOD);
  constant W : natural := clog2(DEPTH);
  signal count : unsigned(W - 1 downto 0);
begin
  reg : process (clk)
  begin
    if rising_edge(clk) then
      if rst = '1' or count = N - 1 then
        count <= (others => '0');
      else
        count <= count + 1;
      end if;
    end if;
  end process;

  q <= '1' when count = 0 else '0';
end architecture;
