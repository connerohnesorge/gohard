-- The DIR_* statuses and item queries: DIR_CREATEDIR, DIR_ITEMEXISTS,
-- DIR_ITEMISDIR, DIR_ITEMISFILE, DIR_WORKINGDIR (a textio file opened
-- after it is relative to the new working directory), DIR_DELETEFILE
-- and DIR_DELETEDIR, in their function and procedure forms. Every
-- write lands in the run's .gohard/run/<hash>/ folder (#18).
use std.env.all;
use std.textio.all;

entity dir_status_tb is
end entity dir_status_tb;

architecture sim of dir_status_tb is
begin
  check : process
    file f : text;
    variable l  : line;
    variable cs : dir_create_status;
    variable os : dir_open_status;
    variable ds : dir_delete_status;
    variable fs : file_delete_status;
  begin
    cs := dir_createdir("work_dir");
    report "createdir: " & dir_create_status'image(cs);
    dir_createdir("work_dir", cs);
    report "createdir again: " & dir_create_status'image(cs);
    cs := dir_createdir("work_dir/a/b");
    report "createdir, no parent: " & dir_create_status'image(cs);
    report "exists " & boolean'image(dir_itemexists("work_dir")) &
      ", isdir " & boolean'image(dir_itemisdir("work_dir")) &
      ", isfile " & boolean'image(dir_itemisfile("work_dir")) &
      ", missing " & boolean'image(dir_itemexists("no_such_item"));
    os := dir_workingdir("work_dir");
    report "workingdir: " & dir_open_status'image(os);
    file_open(f, "out.txt", write_mode);
    write(l, string'("written in work_dir"));
    writeline(f, l);
    file_close(f);
    report "out.txt is a file: " & boolean'image(dir_itemisfile("out.txt"));
    dir_workingdir("..", os);
    report "back: " & dir_open_status'image(os) &
      ", work_dir/out.txt is a file: " &
      boolean'image(dir_itemisfile("work_dir/out.txt"));
    report "workingdir to a missing folder: " &
      dir_open_status'image(dir_workingdir("no_such_item"));
    ds := dir_deletedir("work_dir");
    report "deletedir, not empty: " & dir_delete_status'image(ds);
    fs := dir_deletefile("work_dir/out.txt");
    report "deletefile: " & file_delete_status'image(fs);
    dir_deletefile("work_dir/out.txt", fs);
    report "deletefile again: " & file_delete_status'image(fs);
    ds := dir_deletedir("work_dir");
    report "deletedir: " & dir_delete_status'image(ds) & ", exists " &
      boolean'image(dir_itemexists("work_dir"));
    dir_deletedir("work_dir", ds);
    report "deletedir again: " & dir_delete_status'image(ds);
    wait;
  end process check;
end architecture sim;
