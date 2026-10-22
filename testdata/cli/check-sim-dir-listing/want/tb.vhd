use std.env.all;

-- DIR_OPEN lists a project directory sorted by name ("." and ".."
-- included), whatever order the files were made in; the listing is
-- recorded in the repro file's inputs.
entity tb is
end entity;

architecture sim of tb is
begin
  process
    variable d  : directory;
    variable st : dir_open_status;
  begin
    st := dir_open(d, "data");
    report dir_open_status'image(st) & " " & d.name.all & " " &
      integer'image(d.items'length);
    for i in d.items'range loop
      report d.items(i).all;
    end loop;
    dir_close(d);
    wait;
  end process;
end architecture;
