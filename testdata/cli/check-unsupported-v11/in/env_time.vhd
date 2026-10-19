entity env_time is
end entity env_time;

architecture sim of env_time is
begin
  process
    variable now_rec : std.env.time_record;
  begin
    now_rec := std.env.localtime;
    wait;
  end process;
end architecture sim;
