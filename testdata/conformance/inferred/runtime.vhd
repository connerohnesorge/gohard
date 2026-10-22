-- Inferred constraints from values known only at elaboration or run
-- time: function results give a variable and a signal their ranges,
-- the element constraints of an array and the field constraints of a
-- record included (LRM 6.4.2.1).
entity runtime_tb is
end entity runtime_tb;

architecture sim of runtime_tb is
  type word_array is array (natural range <>) of bit_vector;
  type pair is record
    tag  : string;
    data : bit_vector;
  end record;

  signal size : integer := 3;

  function make(n : natural) return word_array is
    variable r : word_array(0 to n - 1)(n downto 0);
  begin
    return r;
  end function make;

  impure function tagged(n : natural) return pair is
    variable t : string(1 to n);
    variable d : bit_vector(n + 1 downto 0);
  begin
    return (tag => t, data => d);
  end function tagged;

  impure function width return natural is
  begin
    return 5;
  end function width;

  signal words : word_array := make(5);
begin
  process
    variable wa : word_array := make(size + width - 5);
    variable p  : pair := tagged(size);
  begin
    report "wa " & integer'image(wa'length) & " x " &
      integer'image(wa(0)'length) & " " & integer'image(wa(0)'left);
    report "p " & integer'image(p.tag'length) & " " &
      integer'image(p.data'length) & " " & integer'image(p.data'left);
    report "words " & integer'image(words'length) & " x " &
      integer'image(words(4)'length);
    wa(1) := "1010";
    report "wa(1) " & to_string(wa(1));
    p.data := "11";
    report "unreached";
    wait;
  end process;
end architecture sim;
