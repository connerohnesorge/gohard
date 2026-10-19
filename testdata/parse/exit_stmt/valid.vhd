architecture a of e is
begin
  process is
  begin
    loop
      exit;
    end loop;
    o : loop
      exit o when done;
    end loop;
  end process;
end;
