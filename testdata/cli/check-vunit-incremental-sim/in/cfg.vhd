-- A component configuration without an entity aspect (an incremental
-- binding, LRM 7.3.2.1): u1 is bound by tb's configuration
-- specification to dut(rtl), so v_comp is bound to dut(rtl), not to
-- an entity named like the component dut_c.
configuration cfg of tb is
  for sim
    for u1 : dut_c
      use vunit v_comp;
    end for;
  end for;
end configuration;
