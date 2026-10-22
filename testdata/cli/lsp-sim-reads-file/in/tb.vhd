use std.textio.all;

-- A testbench reading a stimulus file below the project root: gohard
-- lsp sim and the editor's run command (the code lens) read the same
-- file and record its hash in the same repro file.
entity tb is
end entity;

architecture sim of tb is
begin
  process
    file f : text;
    variable l : line;
  begin
    file_open(f, "stim.txt", read_mode);
    readline(f, l);
    report "stimulus " & l.all;
    wait;
  end process;
end architecture;
