architecture a of e is
begin
  process is
  begin
    for i in 0 to 3 loop
      next;
      next when i = 1;
    end loop;
    o : for i in 0 to 3 loop
      next o when i = 2;
    end loop;
  end process;
end;
