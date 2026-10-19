architecture a of e is
begin
  process is
  begin
    case sel is
      when "00" => x := 0;
      when others => x := 1;
    end case;
    m : case? sel is
      when "1-" => x := 2;
      when others => null;
    end case? m;
  end process;
end;
