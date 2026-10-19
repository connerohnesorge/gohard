architecture a of e is
begin
  process is
  begin
    s <= transport a after 1 ns;
    t <= reject 2 ns inertial b after 3 ns;
    u <= inertial c;
  end process;
end;
