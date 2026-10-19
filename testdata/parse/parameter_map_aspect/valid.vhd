architecture a of e is
begin
  process is
  begin
    put generic map (t => integer) parameter map (x => 1);
    wait;
  end process;
end;
