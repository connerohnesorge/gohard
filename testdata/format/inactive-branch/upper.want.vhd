ENTITY ca IS
    PORT (a : IN bit; b : OUT bit);
END;
ARCHITECTURE a OF ca IS
BEGIN
`if VHDL_VERSION < "2000" then
     B<=A   ;   -- badly   formatted, inactive
	SIGNAL   foo:BIT;
  END   ARCHITECTURE  ;   
`else
    B <= A;
`end if
END;
