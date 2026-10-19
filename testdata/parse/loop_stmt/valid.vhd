architecture a of e is
begin
  process is
  begin
    l : loop
      exit l when done;
    end loop l;
  end process;
end;
