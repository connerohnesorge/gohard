use work.pkg.all;

entity top is
end entity top;

architecture rtl of top is
  signal s : bit_vector(width - 1 downto 0);
  signal t : integer := inc(1, 2);
begin
  s <= undefined_name;
end architecture rtl;
