entity ca is
end;
architecture a of ca is
begin
`if VHDL_VERSION < "2000" then
     B<=A   ;   -- badly   formatted, inactive
	SIGNAL   foo:BIT;
  END   ARCHITECTURE  ;   
`end if
END;
