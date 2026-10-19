architecture a of e is
begin
  process is
    variable v : integer;
  begin
    v := id generic map (t => integer) (4);
    wait;
  end process;
end;
