-- standin_model: wraps the opaque crypt_core IP behind a component and
-- registers its output. Everything crypt_core drives (u_core's dout and
-- valid, and through them core_dout/core_valid) comes from the stand-in
-- and is marked modeled in the trace.
library ieee;
use ieee.std_logic_1164.all;

entity standin_model is
  port (
    clk   : in  std_logic;
    rst   : in  std_logic;
    din   : in  std_logic_vector(7 downto 0);
    dout  : out std_logic_vector(7 downto 0);
    valid : out std_logic
  );
end entity standin_model;

architecture rtl of standin_model is
  component crypt_core is
    port (
      clk   : in  std_logic;
      rst   : in  std_logic;
      din   : in  std_logic_vector(7 downto 0);
      dout  : out std_logic_vector(7 downto 0);
      valid : out std_logic
    );
  end component crypt_core;

  signal core_dout  : std_logic_vector(7 downto 0);
  signal core_valid : std_logic;
begin
  u_core : component crypt_core
    port map (
      clk   => clk,
      rst   => rst,
      din   => din,
      dout  => core_dout,
      valid => core_valid
    );

  reg : process (clk)
  begin
    if rising_edge(clk) then
      dout  <= core_dout;
      valid <= core_valid;
    end if;
  end process reg;
end architecture rtl;
