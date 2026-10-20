library ieee;
use ieee.std_logic_1164.all;

library altera_mf;
use altera_mf.altera_mf_components.all;

entity ram is
  port (
    clk  : in  std_logic;
    addr : in  std_logic_vector(7 downto 0);
    din  : in  std_logic_vector(15 downto 0);
    we   : in  std_logic;
    dout : out std_logic_vector(15 downto 0)
  );
end entity ram;

architecture rtl of ram is
begin
  u_ram : altsyncram
    generic map (
      operation_mode => "SINGLE_PORT",
      width_a        => 16,
      widthad_a      => 8
    )
    port map (
      clock0    => clk,
      address_a => addr,
      data_a    => din,
      wren_a    => we,
      q_a       => dout
    );
end architecture rtl;
