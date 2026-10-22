-- GET_CALL_PATH from a procedure called from a process and from a
-- function: innermost first, one element per subprogram and one for
-- the process, each at the line it is executing.
use std.env.all;

entity call_path_tb is
end entity call_path_tb;

architecture sim of call_path_tb is
  procedure inner is
    variable p : call_path_vector_ptr;
  begin
    p := GET_CALL_PATH;
    report "depth " & integer'image(p'length);
    for i in p'range loop
      report "depth " & integer'image(p'length) & ", " &
        integer'image(i) & ": " & p(i).name.all & " in " &
        p(i).file_name.all & " line " & integer'image(p(i).file_line);
    end loop;
    deallocate(p);
  end procedure inner;

  impure function outer return integer is
  begin
    inner;
    return 1;
  end function outer;
begin
  check : process
    variable x : integer;
  begin
    inner;
    x := outer;
    wait;
  end process check;
end architecture sim;
