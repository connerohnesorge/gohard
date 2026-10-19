ENTITY LITERALS IS
END ENTITY;
ARCHITECTURE a OF LITERALS IS
  CONSTANT S : string := "  Keep   THIS  , spacing ;  ";
  CONSTANT C : character := ' ';
  CONSTANT B : bit_vector := X"DE_AD" & B"1010" & 12UX"F_f";
  CONSTANT Q : string := "with ""quotes""  inside";
  SIGNAL \Weird  Name\ : bit;
  SIGNAL \END\ : bit;
  /*  a   block   comment   with   END   IS   BEGIN
         keeps    every   byte   */
BEGIN
  \Weird  Name\ <= \END\;
END;
