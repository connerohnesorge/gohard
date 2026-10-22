-- A generic type of a protected type whose actual is an anonymous
-- constrained subtype (bit_vector(0 to 2), string(1 to 3)) keeps the
-- constraint: the body's variables of the generic type are
-- constrained by it, for the variable generic map form and a type
-- instance. The same holds for a subprogram instance.
entity constrained_actual_tb is
end entity constrained_actual_tb;

architecture sim of constrained_actual_tb is
  type box_t is protected
    generic (type elem_t);
    impure function size return natural;
  end protected box_t;

  type box_t is protected body
    variable v : elem_t;
    impure function size return natural is
    begin
      return v'length;
    end function size;
  end protected body box_t;

  type str3_box is new box_t generic map (elem_t => string(1 to 3));

  procedure show generic (type elem_t) parameter (tag : string) is
    variable w : elem_t;
  begin
    report tag & " length " & integer'image(w'length);
  end procedure show;

  procedure show4 is new show
    generic map (elem_t => bit_vector(3 downto 0));
begin
  process
    variable b : box_t generic map (elem_t => bit_vector(0 to 2));
    variable s : str3_box;
  begin
    report "bits " & integer'image(b.size) & " string " &
      integer'image(s.size);
    assert b.size = 3 and s.size = 3 severity failure;
    show4("procedure");
    wait;
  end process;
end architecture sim;
