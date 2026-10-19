package p is
  function int_id is new id generic map (t => integer);
  procedure bit_swap is new swap [bit, bit] generic map (t => bit);
end;
