entity cd is
end;
architecture a of cd is
begin
`if MODE = "fast" then
   ASSERT   FALSE   REPORT  "fast"  ;
`elsif MODE = "slow" then
  assert FALSE report "slow";
`else
      ASSERT   FALSE   REPORT  "none"  ;
`end if
end;
