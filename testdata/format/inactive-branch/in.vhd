entity ca is
  port (a : in bit; b : out bit);
end;
architecture a of ca is
begin
`if VHDL_VERSION < "2000" then
     B<=A   ;   -- badly   formatted, inactive
	SIGNAL   foo:BIT;
  END   ARCHITECTURE  ;   
`else
B<=A;
`end if
end;
