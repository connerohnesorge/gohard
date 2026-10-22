use std.textio.all;

-- Names outside the project root, or inside GoHard's own .gohard
-- directory, are NAME_ERROR when opened; nothing is written there.
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
    if st = open_ok then
      write(l, string'("x"));
      writeline(f, l);
      file_close(f);
    end if;
  end procedure;
begin
  process
  begin
    try("../escape.txt", write_mode);
    try("sub/../../escape.txt", append_mode);
    try("/gohard-outside-root/abs.txt", write_mode);
    try(".gohard/traces/tb-0.trace", read_mode);
    try(".gohard/mine.txt", write_mode);
    try("inside.txt", write_mode);
    wait;
  end process;
end architecture;
