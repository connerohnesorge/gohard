use std.env.all;

entity stop_tb is
end entity;

architecture sim of stop_tb is
begin
  process
  begin
    wait for 1 ns;
    stop(3);
    wait;
  end process;
end architecture;
