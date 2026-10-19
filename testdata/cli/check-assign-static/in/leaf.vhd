entity leaf is
  generic (W : positive := 4);
end;
architecture a of leaf is
  signal x : bit_vector(W - 1 downto 0);
  signal y : bit_vector(3 downto 0);
  signal z : bit_vector(1 downto 0);
  signal b : bit;
begin
  x <= y;    -- wrong only for tb.u (W = 2)
  b <= y(W); -- wrong only for tb.v (W = 4)
  z <= y;    -- wrong in every instance: reported once, with the unit
  process
    variable v : bit_vector(1 downto 0);
  begin
    v := y;
    b <= y(7);
    if W > 8 then
      b <= y(8); -- a branch that may never run is not checked
    end if;
    wait;
  end process;
  g : if W = 2 generate
    z <= y(1 downto 0);
  else generate
    z <= y; -- elaborated for tb.v only
  end generate;
end;
