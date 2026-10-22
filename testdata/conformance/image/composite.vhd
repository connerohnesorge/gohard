-- 'IMAGE of composite types and objects (VHDL-2019, LRM 16.2.2):
-- records, arrays of characters and of integers, nested composites,
-- and O'IMAGE of objects.
entity composite_tb is
end entity composite_tb;

architecture sim of composite_tb is
  type rec_t is record
    n : integer;
    b : bit;
  end record rec_t;

  type int_vec is array (natural range <>) of integer;

  type color is (red, green, blue);
  type nest_t is record
    r : rec_t;
    v : bit_vector(1 to 2);
    c : color;
  end record nest_t;

  constant k : string := rec_t'image((7, '1'));
begin
  p : process
    variable r : rec_t := (7, '1');
    variable bv : bit_vector(3 downto 0) := "1010";
    variable iv : int_vec(0 to 3) := (1, 2, -99, 0);
    variable nr : nest_t := ((-3, '0'), "01", blue);
    variable i : integer := 42;
    variable s : string(1 to 3) := "a""b";
  begin
    report "record " & rec_t'image(r);
    report "bit_vector " & bit_vector'image(bv);
    report "int_vec " & int_vec'image(iv);
    report "nested " & nest_t'image(nr);
    report "string " & string'image(s);
    report "objects " & r'image & " " & bv'image & " " & i'image;
    report "constant " & k;
    assert rec_t'image(r) = "(7,'1')" report "record" severity error;
    assert bit_vector'image(bv) = """1010""" report "bv" severity error;
    assert i'image = "42" report "object" severity error;
    wait;
  end process p;
end architecture sim;
