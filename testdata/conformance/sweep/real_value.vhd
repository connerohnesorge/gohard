-- std: 2008
-- 'VALUE of a floating type (LRM 16.2.2): a decimal literal, with
-- an exponent, optionally signed.
entity real_value_tb is
end entity real_value_tb;

architecture sim of real_value_tb is
begin
  process
    variable r : real;
  begin
    r := real'value("1.5");
    assert r = 1.5 severity failure;
    r := real'value("2.5E-1");
    assert r = 0.25 severity failure;
    r := real'value("-20.25e1");
    assert abs(r + 202.5) < 1.0e-9 severity failure;
    report "value " & integer'image(integer(r * 2.0));
    wait;
  end process;
end architecture sim;
