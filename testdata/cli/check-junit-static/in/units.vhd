-- Two packages declare homographs: a design using both cannot call
-- them without saying which (LRM 12.4, 12.5).
package units_si is
  type length is (mm, m, km);
  function scale(x : integer) return integer;
  procedure log(x : integer);
end package units_si;

package units_us is
  type length is (inch, ft, m);
  function scale(x : integer) return integer;
  procedure log(x : integer);
end package units_us;
