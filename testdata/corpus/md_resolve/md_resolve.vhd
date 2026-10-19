-- md_resolve: multi-driver std_logic resolution and delta ordering.
--
-- The DUT drives the shared inout bus `bus_io` (and the vector `vec_io`)
-- from two concurrent drivers of its own; the testbench adds a third
-- driver on the same nets. A three-stage signal chain `d0 -> d1 -> d2 ->
-- d3` makes each stage settle exactly one delta cycle after the previous
-- one, and a swap process exchanges two signals in a single delta.
library ieee;
use ieee.std_logic_1164.all;

entity md_resolve is
  port (
    a_en   : in    std_logic;
    a_val  : in    std_logic;
    b_en   : in    std_logic;
    b_val  : in    std_logic;
    bus_io : inout std_logic;
    v_en   : in    std_logic;
    vec_io : inout std_logic_vector(3 downto 0);
    d0     : in    std_logic;
    d3     : out   std_logic;
    swap   : in    std_logic;
    p_out  : out   std_logic;
    q_out  : out   std_logic
  );
end entity md_resolve;

architecture rtl of md_resolve is
  signal d1 : std_logic := '0';
  signal d2 : std_logic := '0';
  signal p  : std_logic := '0';
  signal q  : std_logic := '1';
begin
  -- Two concurrent drivers of the same resolved port.
  bus_io <= a_val when a_en = '1' else 'Z';
  bus_io <= b_val when b_en = '1' else 'Z';

  -- One more driver on the vector, element-wise resolved.
  vec_io <= "01HZ" when v_en = '1' else (others => 'Z');

  -- Delta chain: d1 follows d0 one delta later, and so on.
  d1 <= d0;
  d2 <= d1;
  d3 <= d2;

  -- Both assignments read the old values, so p and q swap in one delta.
  swapper : process (swap) is
  begin
    if rising_edge(swap) then
      p <= q;
      q <= p;
    end if;
  end process swapper;

  p_out <= p;
  q_out <= q;
end architecture rtl;
