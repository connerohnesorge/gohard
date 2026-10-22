-- FILE_VALUE_MIRROR and FILE_SUBTYPE_MIRROR of an open file.
use std.reflection.all;

entity file_tb is
end entity file_tb;

architecture sim of file_tb is
  type ifile is file of integer;
  file f : ifile;
begin
  check : process
  begin
    file_open(f, "data.bin", write_mode);
    report "subtype " & f'reflect.get_subtype_mirror.simple_name &
      ", designated " &
      f'reflect.to_file.get_subtype_mirror.designated_subtype.simple_name;
    report "logical name " & f'reflect.to_file.get_file_logical_name &
      ", open kind " &
      file_open_kind'image(f'reflect.to_file.get_file_open_kind);
    file_close(f);
    wait;
  end process check;
end architecture sim;
