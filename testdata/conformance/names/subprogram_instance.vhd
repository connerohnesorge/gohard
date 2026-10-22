-- The names of a declaration of a subprogram instance (LRM 4.4, 16.2.5):
-- the instance is equivalent to a subprogram declared with its own
-- designator, so 'PATH_NAME and 'INSTANCE_NAME name the instance, not
-- the uninstantiated subprogram (as NVC 1.23).
entity names_subprogram_instance_tb is
end entity names_subprogram_instance_tb;

architecture sim of names_subprogram_instance_tb is
  procedure pp generic (n : integer) parameter (x : integer) is
    variable w : bit;
  begin
    report "pp " & w'path_name & " | " & w'instance_name;
  end procedure pp;

  procedure q is new pp generic map (n => 1);

  function ff generic (n : integer) return string is
    variable w : bit;
  begin
    return w'path_name & " | " & w'instance_name;
  end function ff;

  function fi is new ff generic map (n => 2);
begin
  main : process
  begin
    q(1);
    report "fi " & fi;
    wait;
  end process main;
end architecture sim;
