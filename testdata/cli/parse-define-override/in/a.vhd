`if PROFILE = "wide" then
entity a is end;
`else
entity b is end;
`end if
`if VHDL_VERSION < "2019" then
`error "needs 2019"
`end if
