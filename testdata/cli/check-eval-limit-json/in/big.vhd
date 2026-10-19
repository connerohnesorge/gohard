package big is
  -- Two million elements: more than the 1,000,000-step limit of static
  -- evaluation in the editor and gohard check (#34).
  constant ones : bit_vector(0 to 1999999) := (others => '1');
end package big;
