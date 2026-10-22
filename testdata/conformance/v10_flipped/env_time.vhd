-- nvc-differs: NVC's clock is the wall clock; GoHard's is
-- simulation.env.epoch plus simulated time
-- The v1.0 fixture check-unsupported-v11/in/env_time.vhd, now a v1.1
-- simulation: LOCALTIME at the default epoch, 2000-01-01 UTC.
entity env_time is
end entity env_time;

architecture sim of env_time is
begin
  process
    variable now_rec : std.env.time_record;
  begin
    now_rec := std.env.localtime;
    report std.env.to_string(now_rec);
    assert now_rec.year = 2000 report "year" severity error;
    wait;
  end process;
end architecture sim;
