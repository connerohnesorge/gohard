-- nvc-differs: NVC lists a directory in readdir order and names it by its absolute path; GoHard sorts the items by name and names directories relative to the project root (#18)
-- DIR_OPEN lists a directory sorted by name, "." and ".." included,
-- whatever order its items were made in. The run makes the directory
-- itself (DIR_CREATEDIR and textio writes), so the listing does not
-- depend on the folder the case runs in; the writes land in the run's
-- .gohard/run/<hash>/ folder, never in the project.
use std.env.all;
use std.textio.all;

entity dir_listing_tb is
end entity dir_listing_tb;

architecture sim of dir_listing_tb is
  procedure touch(name : string) is
    file f : text;
    variable l : line;
  begin
    file_open(f, name, write_mode);
    write(l, name);
    writeline(f, l);
    file_close(f);
  end procedure touch;
begin
  check : process
    variable d  : directory;
    variable st : dir_open_status;
    variable l  : line;
  begin
    assert dir_createdir("scratch") = status_ok;
    touch("scratch/zeta.txt");
    assert dir_createdir("scratch/mid") = status_ok;
    touch("scratch/Beta.txt");
    touch("scratch/alpha.txt");
    st := dir_open(d, "scratch");
    report "DIR_OPEN: " & dir_open_status'image(st) & ", name " &
      d.name.all & ", " & integer'image(d.items'length) & " items";
    for i in d.items'range loop
      write(l, ' ');
      write(l, d.items(i).all);
    end loop;
    report "items:" & l.all;
    for i in d.items'low to d.items'high - 1 loop
      assert d.items(i).all < d.items(i + 1).all
        report "not sorted at " & integer'image(i);
    end loop;
    dir_close(d);
    wait;
  end process check;
end architecture sim;
