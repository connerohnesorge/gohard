-- test: kind=VariableDecl,GenericMapAspect
architecture a of e is
  shared variable sv : pt generic map (n => 4);
begin
  process is
    variable v1 : pt generic map (n => 8, t => integer);
    variable v2 : pt generic map (16) := init;
  begin
    wait;
  end process;
end;
