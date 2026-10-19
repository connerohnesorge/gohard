entity leaf is
  generic (WIDTH : positive := 8);
  port (d : in bit_vector(WIDTH - 1 downto 0));
end entity leaf;

architecture rtl of leaf is
begin
end architecture rtl;

entity dut is
  generic (WIDTH : positive := 8);
  port (bus_in : in bit_vector(11 downto 0));
end entity dut;

architecture rtl of dut is
begin
  u0: entity work.leaf generic map (WIDTH => 12) port map (d => bus_in);
  u1: entity work.leaf generic map (WIDTH => WIDTH) port map (d => bus_in);
end architecture rtl;

entity tb is
end entity tb;

architecture sim of tb is
  signal s : bit_vector(11 downto 0);
begin
  dut: entity work.dut generic map (WIDTH => 10) port map (bus_in => s);
end architecture sim;
