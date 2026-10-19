entity top is
end entity top;

architecture rtl of top is
  signal a, b : bit_vector(7 downto 0);
begin
  u : entity work.core
    port map (d => a, q => b);
end architecture rtl;
