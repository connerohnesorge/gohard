entity env_assert is
end entity env_assert;

architecture sim of env_assert is
begin
  process
  begin
    assert std.env.GetVhdlAssertCount = 0;
    wait;
  end process;
end architecture sim;
