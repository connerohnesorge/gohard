-- Declarations whose outline kinds the symbol tests lock (#116).
context demo is
  library ieee;
  use ieee.std_logic_1164.all;
end context demo;

library ieee;
use ieee.std_logic_1164.all;

package gp is
  generic (n : integer := 1);
  constant bias : integer := n;
  procedure show;
end package gp;

package body gp is
  procedure show is
  begin
    null;
  end procedure show;
end package body gp;

package gp3 is new work.gp generic map (n => 3);

library ieee;
use ieee.std_logic_1164.all;

package ops is
  type state_t is (idle, busy);
  type pair_t is record
    first  : integer;
    second : std_logic;
  end record pair_t;
  type span_t is range 0 to 1000000
    units
      tick;
      tock = 1000 tick;
    end units span_t;
  subtype byte_t is integer range 0 to 255;
  constant ZERO : integer := 0;
  alias NIL is ZERO;
  attribute keep : boolean;
  function inc(x : integer) return integer;
  procedure step(signal x : inout integer);
  component leaf is
    generic (WIDTH : positive := 8);
    port (clk : in std_logic);
  end component leaf;
end package ops;

package body ops is
  function inc(x : integer) return integer is
  begin
    return x + 1;
  end function inc;

  procedure step(signal x : inout integer) is
  begin
    x <= x + 1;
  end procedure step;
end package body ops;

library ieee;
use ieee.std_logic_1164.all;

entity boxed is
  generic (
    type data_t;
    function same(a, b : data_t) return boolean
  );
end entity boxed;

library ieee;
use ieee.std_logic_1164.all;
use std.textio.all;
use work.ops.all;

entity top is
  generic (WIDTH : positive := 8);
  port (
    clk : in  std_logic;
    q   : out integer
  );
end entity top;

architecture rtl of top is
  signal n : integer;
  constant LIMIT : integer := 4;
  file logf : text;
begin
  u0 : leaf
    generic map (WIDTH => WIDTH)
    port map (clk => clk);

  g : for i in 0 to 1 generate
    assert i < LIMIT;
  end generate g;

  wide : if hi : WIDTH > 1 generate
    assert true;
  else lo : generate
    assert true;
  end generate wide;

  blk : block is
    constant K : integer := 1;
  begin
    assert K = 1;
  end block blk;

  p : process (clk)
    variable v : integer := 0;
  begin
    lp : for i in 0 to 1 loop
      v := v + i;
    end loop lp;
    if rising_edge(clk) then
      q <= v;
    end if;
  end process p;

  call_step : step(n);
end architecture rtl;

configuration top_cfg of top is
  for rtl
  end for;
end configuration top_cfg;
