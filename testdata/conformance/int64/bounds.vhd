-- 64-bit INTEGER in VHDL-2019 (LRM 5.2.3.2): 2**40, the bounds of
-- INTEGER, NATURAL and POSITIVE, and arithmetic past 32 bits.
entity big_tb is
end entity big_tb;

architecture sim of big_tb is
  constant c40 : integer := 2 ** 40;

  function twice (x : integer) return integer is
  begin
    return 2 * x;
  end function twice;

  signal s : natural := 2 ** 33;
begin
  p : process
    variable v : integer := c40;
    variable n : natural;
  begin
    report "2**40 = " & integer'image(c40);
    report "integer'high = " & integer'image(integer'high);
    report "integer'low = " & integer'image(integer'low);
    report "natural'high = " & integer'image(natural'high);
    report "positive'low = " & integer'image(positive'low);
    v := v * 1024 + 7;
    report "v = " & integer'image(v);
    n := twice(c40) - 1;
    report "twice - 1 = " & integer'image(n);
    report "s = " & integer'image(s);
    assert integer'high = 9223372036854775807 report "high" severity error;
    assert integer'low = -9223372036854775807 - 1 report "low"
      severity error;
    assert v = 1125899906842631 report "v" severity error;
    assert s / 2 ** 32 = 2 report "s" severity error;
    wait;
  end process p;
end architecture sim;
