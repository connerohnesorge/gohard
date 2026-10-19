entity top is end;
architecture rtl of top is
  component core is
    port (d : in bit_vector(7 downto 0); q : out bit_vector(7 downto 0));
  end component;
  signal a, b : bit_vector(7 downto 0);
begin
  u : core port map (d => a, q => b);
end;
