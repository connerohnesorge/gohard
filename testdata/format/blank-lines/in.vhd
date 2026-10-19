


library ieee;



use ieee.std_logic_1164.all;
entity bl is

  port (a : in std_logic);


end;



architecture a of bl is

  signal s : std_logic;



  signal t : std_logic;

begin

  s <= a;


  t <= s;

end;



