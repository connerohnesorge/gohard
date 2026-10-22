-- nvc-differs: NVC writes the absolute directory of the file
-- The TO_STRING forms of a call path: "path/file:line:NAME", with the
-- project-relative path, joined by the separator.
use std.env.all;

entity call_path_string_tb is
end entity call_path_string_tb;

architecture sim of call_path_string_tb is
  procedure inner is
    variable p : call_path_vector_ptr;
  begin
    p := GET_CALL_PATH;
    report "pointer: " & TO_STRING(p, " | ");
    report "vector: " & TO_STRING(p.all, "; ");
    report "element: " & TO_STRING(p(0));
    report "path: """ & p(0).file_path.all & """";
    deallocate(p);
  end procedure inner;
begin
  check : process
  begin
    inner;
    wait;
  end process check;
end architecture sim;
