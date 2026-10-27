library ieee;
use ieee.std_logic_1164.all;
use std.textio.all;

entity top is
  port (
    addr : in  natural range 0 to 3;
    q    : out std_ulogic_vector(7 downto 0)
  );
end entity;

architecture rtl of top is
  type rom_t is array (0 to 3) of std_ulogic_vector(7 downto 0);

  -- Read once while elaborating: the file only sets constant contents.
  impure function load (name : string) return rom_t is
    file f : text open read_mode is name;
    variable l : line;
    variable v : rom_t;
  begin
    for i in rom_t'range loop
      readline(f, l);
      read(l, v(i));
    end loop;
    return v;
  end function;

  constant rom : rom_t := load("rom.txt");
begin
  q <= rom(addr);
end architecture;
