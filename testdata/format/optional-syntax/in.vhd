entity e1 is
end;
entity e2 is
end entity;
entity e3 is
end entity e3;
entity e4 is
end e4;
architecture a of e4 is
begin
  p1 : process
  begin
    wait;
  end process;
  p2 : process is
  begin
    wait;
  end process p2;
  b1 : block
  begin
  end block b1;
  b2 : block is
  begin
  end block;
end;
package pk is
  component c
  end component;
  component d is
  end component d;
end package;
package body pk is
end package body pk;
