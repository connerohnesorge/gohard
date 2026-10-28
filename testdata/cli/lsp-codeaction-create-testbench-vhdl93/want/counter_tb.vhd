library ieee;
use ieee.std_logic_1164.all;
use ieee.numeric_std.all;

entity counter_tb is
end entity counter_tb;

architecture sim of counter_tb is
  constant WIDTH : positive := 8;
  constant CLK_PERIOD : time := 10 ns;
  signal clk : std_logic;
  signal rst : std_logic;
  signal en : std_logic := '1';
  signal count : unsigned(WIDTH - 1 downto 0);
  signal done : boolean := false;
begin
  u_counter : entity work.counter
    generic map (
      WIDTH => WIDTH
    )
    port map (
      clk   => clk,
      rst   => rst,
      en    => en,
      count => count
    );

  clk_gen : process
  begin
    while not done loop
      clk <= '0';
      wait for CLK_PERIOD / 2;
      clk <= '1';
      wait for CLK_PERIOD / 2;
    end loop;
    wait;
  end process clk_gen;

  stimulus : process
  begin
    wait for 100 * CLK_PERIOD;
    done <= true;
    wait;
  end process stimulus;
end architecture sim;
