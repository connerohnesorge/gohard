-- ext_vars: counts the rising edges of a in the shared variable hits
-- of its architecture, and adds C_STEP to the package variable total
-- on each (#63).
use work.ext_vars_pkg.all;
use work.ext_vars_obj.all;

entity ext_vars is
  port (
    a : in bit
  );
end entity ext_vars;

architecture rtl of ext_vars is
  shared variable hits : counter_t;
begin
  count : process (a) is
  begin
    if a = '1' then
      hits.add(1);
      total.add(C_STEP);
    end if;
  end process count;
end architecture rtl;
