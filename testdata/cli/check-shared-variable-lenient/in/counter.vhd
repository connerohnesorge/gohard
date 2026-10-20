-- A VHDL-2008 shared variable that is not of a protected type: an
-- error, or a sema/relaxed warning under lenient: true (GHDL -frelaxed,
-- NVC --relaxed).
package counter is
  shared variable count : integer := 0;
end package counter;
