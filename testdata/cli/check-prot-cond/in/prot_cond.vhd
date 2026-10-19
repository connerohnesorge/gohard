-- prot_cond: entity with a shared-variable port of protected type.
-- It increments the counter three times in one loop, in the same delta.
use work.prot_cond_pkg.all;

entity prot_cond is
  port (
    variable c : inout counter_t
  );
end entity prot_cond;

architecture rtl of prot_cond is
begin
  process
  begin
    for i in 1 to 3 loop
      c.inc;
    end loop;
    wait;
  end process;
end architecture rtl;
