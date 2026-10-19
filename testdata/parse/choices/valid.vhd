architecture a of e is
begin
  process is
  begin
    case n is
      when 0 | 2 | 4 to 6 | state'low => null;
      when others => null;
    end case;
  end process;
end;
