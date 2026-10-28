-- overload_ops: a word type with no predefined arithmetic and two
-- packages that overload "+" for it with different bodies. Every result
-- carries the tag of the body that produced it, so the testbench can
-- tell which overload the use clauses selected.
--   ow_sat:  "+"(word, word) -> 'S' (saturating)
--            "+"(word, integer) -> 'I' (saturating)
--            "-"(word) return word -> 'N', "-"(word) return integer
--   ow_wrap: "+"(word, word) -> 'W' (wraps mod 256)
package ow_types is
  type word_t is record
    v   : integer range 0 to 255;
    src : character;
  end record word_t;

  function w(i : integer) return word_t;
end package ow_types;

package body ow_types is
  function w(i : integer) return word_t is
  begin
    return (v => i, src => '-');
  end function w;
end package body ow_types;

use work.ow_types.all;

package ow_sat is
  function "+"(a, b : word_t) return word_t;
  function "+"(a : word_t; b : integer) return word_t;
  function "-"(a : word_t) return word_t;
  function "-"(a : word_t) return integer;
end package ow_sat;

package body ow_sat is
  function sat(i : integer) return integer is
  begin
    if i > 255 then
      return 255;
    elsif i < 0 then
      return 0;
    end if;
    return i;
  end function sat;

  function "+"(a, b : word_t) return word_t is
  begin
    return (v => sat(a.v + b.v), src => 'S');
  end function "+";

  function "+"(a : word_t; b : integer) return word_t is
  begin
    return (v => sat(a.v + b), src => 'I');
  end function "+";

  function "-"(a : word_t) return word_t is
  begin
    return (v => 255 - a.v, src => 'N');
  end function "-";

  function "-"(a : word_t) return integer is
  begin
    return -a.v;
  end function "-";
end package body ow_sat;

use work.ow_types.all;

package ow_wrap is
  function "+"(a, b : word_t) return word_t;
end package ow_wrap;

package body ow_wrap is
  function "+"(a, b : word_t) return word_t is
  begin
    return (v => (a.v + b.v) mod 256, src => 'W');
  end function "+";
end package body ow_wrap;
