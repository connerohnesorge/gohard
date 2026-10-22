entity env_psl is
end entity env_psl;

architecture sim of env_psl is
begin
  process
  begin
    assert not std.env.PslAssertFailed;
    wait;
  end process;
end architecture sim;
