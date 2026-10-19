architecture a of e is
begin
  process is
  begin
    case n is
      when 0 => x := 0; y := 0;
      when 1 to 3 => x := 1;
      when others => null;
    end case;
  end process;
end;
