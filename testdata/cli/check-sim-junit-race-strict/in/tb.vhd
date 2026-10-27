entity tb is
end entity;

architecture sim of tb is
  shared variable v : integer := 0;
  signal s : integer := 0;
begin
  w : process
  begin
    v := 1;
    wait;
  end process;
  r : process
  begin
    s <= v;
    wait;
  end process;
end architecture;
