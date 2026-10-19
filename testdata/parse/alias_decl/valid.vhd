package p is
  alias word : bit_vector(15 downto 0) is mem(31 downto 16);
  alias s is sig;
  alias plus is "+" [integer, integer return integer];
  alias lit is '0' [return bit];
end;
