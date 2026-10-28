-- ext_vars_pkg: the protected counter of the ext_vars corpus design
-- (#63), and ext_vars_obj, a package with objects the testbench names
-- through package paths. A shared variable of a protected type may only
-- follow the type's body (LRM 14.4.2), so the objects are in a second
-- package.
package ext_vars_pkg is
  type counter_t is protected
    procedure add (n : integer);
    impure function get return integer;
  end protected counter_t;
end package ext_vars_pkg;

package body ext_vars_pkg is
  type counter_t is protected body
    variable v : integer := 0;

    procedure add (n : integer) is
    begin
      v := v + n;
    end procedure add;

    impure function get return integer is
    begin
      return v;
    end function get;
  end protected body counter_t;
end package body ext_vars_pkg;

use work.ext_vars_pkg.all;

package ext_vars_obj is
  constant C_STEP : integer := 5;
  shared variable total : counter_t;
end package ext_vars_obj;
