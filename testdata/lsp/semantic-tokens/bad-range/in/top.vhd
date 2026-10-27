entity top is
  port (q : out bit);
end entity;

architecture rtl of top is
  constant K : integer := 3;
begin
  `if MODE = "fast" then
  q <= '1';
  `elsif MODE = "slow" then
  q <= '0'; -- slow
    -- an indented comment
  `else
  q <= bad name;
  `end if
end architecture;
