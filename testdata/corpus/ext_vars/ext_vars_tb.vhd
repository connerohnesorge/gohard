-- ext_vars_tb: self-checking testbench for ext_vars (#63). It reaches
-- the architecture's shared variable through an absolute external
-- variable name, and the package's shared variable and constant
-- through package paths (@work.ext_vars_obj). A method call through an
-- external name acts on the object itself. Ends with std.env.finish.
use work.ext_vars_pkg.all;
-- GHDL elaborates only the packages a design unit uses, and a package
-- path names an elaborated package.
use work.ext_vars_obj.all;

entity ext_vars_tb is
end entity ext_vars_tb;

architecture tb of ext_vars_tb is
  signal a : bit := '0';
begin
  -- The instance comes first: an external name may only denote an
  -- object that is already elaborated.
  dut : entity work.ext_vars
    port map (a => a);

  stim : process is
    alias hits is <<variable .ext_vars_tb.dut.hits : counter_t>>;
    alias total_ext is <<variable @work.ext_vars_obj.total : counter_t>>;
    alias step is <<constant @work.ext_vars_obj.C_STEP : integer>>;
  begin
    assert step = 5 report "package-path constant" severity error;
    for i in 1 to 3 loop
      a <= '1';
      wait for 1 ns;
      a <= '0';
      wait for 1 ns;
    end loop;
    assert hits.get = 3 report "external variable" severity error;
    assert total_ext.get = 3 * step report "package-path variable"
      severity error;

    -- Through the external names the testbench changes the objects
    -- the design counts in.
    hits.add(10);
    total_ext.add(-15);
    a <= '1';
    wait for 1 ns;
    assert hits.get = 14 report "external variable after add"
      severity error;
    assert total.get = 5 report "package variable after add"
      severity error;

    std.env.finish;
    wait;
  end process stim;
end architecture tb;
