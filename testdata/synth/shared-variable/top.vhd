library ieee;
use ieee.std_logic_1164.all;

entity top is
  port (
    clk : in  std_ulogic;
    inc : in  std_ulogic;
    q   : out natural
  );
end entity;

architecture rtl of top is
  type counter_t is protected
    procedure bump;
    impure function get return natural;
  end protected;

  type counter_t is protected body
    variable n : natural := 0;
    procedure bump is
    begin
      n := n + 1;
    end procedure;
    impure function get return natural is
    begin
      return n;
    end function;
  end protected body;

  shared variable counter : counter_t;
begin
  writer : process (clk)
  begin
    if rising_edge(clk) then
      if inc = '1' then
        counter.bump;
      end if;
    end if;
  end process;

  reader : process (clk)
  begin
    if rising_edge(clk) then
      q <= counter.get;
    end if;
  end process;
end architecture;
