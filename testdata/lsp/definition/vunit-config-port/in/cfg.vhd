configuration cfg of tb is
  -- Binds v_top to tb(sim), the root of the design.
  use vunit v_top;
  for sim
    -- Binds v_comp to u1, bound to dut(rtl).
    for u1 : dut
      use entity work.dut(rtl);
      use vunit v_comp;
    end for;
  end for;
end configuration;
