-- std: 2008
-- A protected type counter shared by two processes (VHDL-2008).
entity counter_tb is
end entity counter_tb;

architecture sim of counter_tb is
  type counter_t is protected
    procedure incr(by : integer);
    impure function value return integer;
  end protected counter_t;

  type counter_t is protected body
    variable count : integer := 0;

    procedure incr(by : integer) is
    begin
      count := count + by;
    end procedure incr;

    impure function value return integer is
    begin
      return count;
    end function value;
  end protected body counter_t;

  shared variable c : counter_t;
begin
  a : process
  begin
    for i in 1 to 3 loop
      c.incr(1);
      wait for 1 ns;
    end loop;
    wait;
  end process a;

  b : process
  begin
    wait for 10 ns;
    c.incr(10);
    report "count = " & integer'image(c.value);
    assert c.value = 13 report "wrong count" severity error;
    wait;
  end process b;
end architecture sim;
