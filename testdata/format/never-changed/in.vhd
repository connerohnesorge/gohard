entity   LITERALS   is
end   entity;
architecture   a   of   LITERALS   is
  constant  S  :  string  :=  "  Keep   THIS  , spacing ;  ";
  constant  C  :  character  :=  ' ';
  constant  B  :  bit_vector  :=  X"DE_AD"  &  B"1010"  &  12UX"F_f";
  constant  Q  :  string  :=  "with ""quotes""  inside";
  signal  \Weird  Name\  :  bit;
  signal  \END\  :  bit;
  /*  a   block   comment   with   END   IS   BEGIN
         keeps    every   byte   */
begin
  \Weird  Name\  <=  \END\;
end;
