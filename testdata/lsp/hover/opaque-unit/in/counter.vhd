-- A plain protected type the other files build on.
package counter is
  type counter_t is protected
    procedure inc;
    impure function get return integer;
  end protected counter_t;
end package counter;

package body counter is
  type counter_t is protected body
    variable n : integer := 0;
    procedure inc is
    begin
      n := n + 1;
    end procedure inc;
    impure function get return integer is
    begin
      return n;
    end function get;
  end protected body counter_t;
end package body counter;
