use work.p.all;

entity top is
  port (variable c : inout counter_t);
end entity top;

architecture rtl of top is
  signal s : bit_vector := "1010";
begin
end architecture rtl;
