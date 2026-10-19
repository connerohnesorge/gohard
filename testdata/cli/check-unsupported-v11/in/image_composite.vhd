package image_composite is
  constant v : bit_vector(0 to 3) := "1010";
  constant s : string := bit_vector'image(v);
end package image_composite;
