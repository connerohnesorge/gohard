-- The v1.0 fixture check-unsupported-v11/in/inferred.vhd, now a v1.1
-- simulation: a signal takes its constraint from its initial value.
entity inferred is
end entity inferred;

architecture sim of inferred is
  signal s : bit_vector := "1010";
begin
  process
  begin
    report "s'length = " & integer'image(s'length) & ", s'left = " &
      integer'image(s'left);
    assert s'length = 4 and s = "1010" report "inferred" severity error;
    wait;
  end process;
end architecture sim;
