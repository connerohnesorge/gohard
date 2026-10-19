entity wr is
  port (clk : in bit; first_input_signal : in bit; second_input_signal : in bit; result_output : out bit);
end entity;
architecture rtl of wr is
  signal sum_of_values : integer;
  constant MESSAGE : string := "this string literal is a single unbreakable piece that is longer than any limit";
begin
  sum_of_values <= first_value + second_value + third_value + fourth_value + fifth_value; -- trailing comment
  u_inst : entity work.child generic map (DATA_WIDTH => 32, ADDR_WIDTH => 12, USE_RESET => true) port map (clk => clk, a => first_input_signal, b => second_input_signal, y => result_output);
  result_output <= first_input_signal and second_input_signal;
end architecture;
