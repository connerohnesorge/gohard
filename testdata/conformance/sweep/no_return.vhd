-- std: 2008
-- A function whose call reaches the end of its body without a return
-- statement is an error of the design (LRM 4.2): a run-time error of
-- the simulation (exit 1), the same for a pure function the static
-- evaluator runs and an impure one the interpreter runs; it is never
-- a GoHard gap (unsupported/construct).
entity no_return_tb is
end entity no_return_tb;

architecture sim of no_return_tb is
  function f(x : integer) return integer is
  begin
    if x > 10 then
      return 1;
    end if;
  end function f;
begin
  process
  begin
    report "f(11) = " & integer'image(f(11));
    report "f(1) = " & integer'image(f(1));
    report "not reached";
    wait;
  end process;
end architecture sim;
