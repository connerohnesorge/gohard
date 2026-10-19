package p is
  subtype w is bit_vector(7 downto 0);
  subtype m is mem_t(0 to 3)(7 downto 0);
  subtype o is mem_t(open)(15 downto 0);
end;
