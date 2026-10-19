-- textio_env_tb: self-checking textio and std.env testbench (#8).
-- Writes a log file with std.textio and std_logic_textio, closes it,
-- reads it back and checks every field, prints one line to OUTPUT and
-- ends with std.env.finish. The log is output only (written into the
-- current directory, then read back), so a replay needs no input file.
library ieee;
use ieee.std_logic_1164.all;
use ieee.std_logic_textio.all;
use std.textio.all;

entity textio_env_tb is
end entity textio_env_tb;

architecture sim of textio_env_tb is
  type vec_array is array (natural range <>) of std_logic_vector(7 downto 0);
  constant stimuli : vec_array := (x"00", x"a5", x"3c", x"ff");
  constant log_name : string := "textio_env.log";
  signal a : std_logic_vector(7 downto 0) := (others => '0');
  signal y : std_logic_vector(7 downto 0);
begin
  dut : entity work.textio_env
    port map (a => a, y => y);

  check : process
    file log : text;
    variable l : line;
    variable status : file_open_status;
    variable index : integer;
    variable tag : string(1 to 4);
    variable got_a : std_logic_vector(7 downto 0);
    variable got_y : std_logic_vector(7 downto 0);
    variable got_t : time;
    variable ok : boolean;
    variable rows : natural := 0;
  begin
    file_open(status, log, log_name, write_mode);
    assert status = open_ok report "cannot create log" severity failure;
    for i in stimuli'range loop
      a <= stimuli(i);
      wait for 10 ns;
      write(l, i);
      write(l, string'(" vec "));
      hwrite(l, a);
      write(l, ' ');
      write(l, y);
      write(l, ' ');
      write(l, now);
      writeline(log, l);
    end loop;
    file_close(log);

    file_open(status, log, log_name, read_mode);
    assert status = open_ok report "cannot reopen log" severity failure;
    while not endfile(log) loop
      readline(log, l);
      read(l, index, ok);
      assert ok and index = rows report "bad index" severity failure;
      read(l, tag);
      assert tag = " vec" report "bad tag" severity failure;
      hread(l, got_a, ok);
      assert ok and got_a = stimuli(rows)
        report "bad hex field" severity failure;
      read(l, got_y, ok);
      assert ok and got_y = not stimuli(rows)
        report "bad binary field" severity failure;
      read(l, got_t, ok);
      assert ok and got_t = (rows + 1) * 10 ns
        report "bad time field" severity failure;
      rows := rows + 1;
    end loop;
    file_close(log);
    assert rows = stimuli'length report "bad row count" severity failure;

    -- Read from an in-memory line as well as from a file.
    l := new string'("  42 0110");
    read(l, index, ok);
    assert ok and index = 42 report "bad line integer" severity failure;
    read(l, got_a(3 downto 0), ok);
    assert ok and got_a(3 downto 0) = "0110"
      report "bad line vector" severity failure;
    deallocate(l);

    assert std.env.resolution_limit <= 1 ns
      report "bad resolution limit" severity failure;
    write(l, string'("textio_env: rows="));
    write(l, rows);
    writeline(output, l);
    std.env.finish;
    wait;
  end process check;
end architecture sim;
