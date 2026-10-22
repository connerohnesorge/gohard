use std.textio.all;

-- A symbolic link is followed only while it stays in the sandbox: a
-- link into GoHard's own .gohard directory is NAME_ERROR in every
-- mode, a link inside the project reads its target. A directory (the
-- empty name is the project root) is NAME_ERROR in every mode.
entity tb is
end entity;

architecture sim of tb is
  procedure try(name : string; kind : file_open_kind) is
    file f : text;
    variable st : file_open_status;
    variable l : line;
  begin
    file_open(st, f, name, kind);
    report name & ": " & file_open_status'image(st);
    if st = open_ok and kind = read_mode then
      readline(f, l);
      report name & " read: " & l.all;
    elsif st = open_ok then
      write(l, string'("x"));
      writeline(f, l);
    end if;
  end procedure;
begin
  process
  begin
    try("gh/traces/tb-0.trace", read_mode);
    try("gh/new.txt", write_mode);
    try("data/ok.txt", read_mode);
    try("data", write_mode);
    try("data", append_mode);
    try("", write_mode);
    wait;
  end process;
end architecture;
