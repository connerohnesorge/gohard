-- Plain VHDL stand-in for the encrypted work.core.
entity core is
  port (
    d : in  bit_vector(7 downto 0);
    q : out bit_vector(7 downto 0)
  );
end entity core;

architecture model of core is
begin
  q <= not d;
end architecture model;
