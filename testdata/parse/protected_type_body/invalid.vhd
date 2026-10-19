package body p is
  type counter is protected body
    variable n : natural := 0;
  end protected counter;
end;
