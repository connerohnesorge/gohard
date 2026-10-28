-- dut: a case statement, a conditional and a selected concurrent
-- assignment (#62).
use work.util.all;

entity dut is
  port (
    mode : in  integer;
    x    : in  integer;
    y    : out integer;
    flag : out bit
  );
end entity dut;

architecture rtl of dut is
  signal kind : bit_vector(1 downto 0);
begin
  calc : process (mode, x)
    variable v : integer;
  begin
    case mode is
      when 0 => v := x;
      when 1 => v := clamp(x, 0, 10);
      when others => v := -x;
    end case;
    y <= v;
  end process calc;

  kind <= "01" when x < 0 else "10" when x > 100 else "00";

  with kind select
    flag <= '1' when "01" | "10",
            '0' when others;
end architecture rtl;
