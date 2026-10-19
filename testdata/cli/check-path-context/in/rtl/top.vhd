use work.defs.all;

entity top is
  port (d : in bit_vector(WIDTH - 1 downto 0));
end entity top;

architecture rtl of top is
  signal s : bit := nope;
begin
end architecture rtl;
