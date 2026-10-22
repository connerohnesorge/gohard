-- Generic clauses on protected types (VHDL-2019, LRM 5.6.2,
-- 6.4.2.4): two variables of one generic protected type, mapped when
-- they are declared, and a shared variable using the defaults, each
-- bind their own generic values; a default reads the generic before
-- it, and a type declared in the body is sized by a generic. NVC 1.23
-- does not parse protected generics, so there is no NVC recording.
entity objects_tb is
end entity objects_tb;

architecture sim of objects_tb is
  type counter_t is protected
    generic (step : integer := 1; size : integer := step * 4);
    procedure bump;
    impure function get return integer;
    impure function width return integer;
  end protected counter_t;

  type counter_t is protected body
    type buf_t is array (0 to size - 1) of integer;
    variable buf : buf_t;
    variable count : integer := 0;

    procedure bump is
    begin
      count := count + step;
    end procedure bump;

    impure function get return integer is
    begin
      return count;
    end function get;

    impure function width return integer is
    begin
      return buf'length;
    end function width;
  end protected body counter_t;

  shared variable plain : counter_t;
begin
  check : process
    variable by2 : counter_t generic map (step => 2);
    variable by5 : counter_t generic map (5, 3);
  begin
    by2.bump;
    by2.bump;
    by5.bump;
    plain.bump;
    report "by2 = " & integer'image(by2.get) & ", width " &
      integer'image(by2.width);
    report "by5 = " & integer'image(by5.get) & ", width " &
      integer'image(by5.width);
    report "plain = " & integer'image(plain.get) & ", width " &
      integer'image(plain.width);
    assert by2.get = 4 and by5.get = 5 and plain.get = 1;
    assert by2.width = 8 and by5.width = 3 and plain.width = 4;
    wait;
  end process check;
end architecture sim;
