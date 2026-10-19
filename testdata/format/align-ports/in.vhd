entity al is
  generic (
    WIDTH : natural := 8;
    DEPTH_LOG2 : natural := 4;
    -- a comment-only line ends an alignment block
    G : boolean := false;

    LONGER_NAME_AFTER_BLANK : integer := 0
  );
  port (
    clk : in bit;
    reset_n : in bit;
    data_in : in bit_vector(7 downto 0);
    q : out bit
  );
end entity;
architecture rtl of al is
  signal a : bit;
  signal bb : bit;
  signal ccc : bit_vector(7 downto 0);
  constant table : bit_vector(3 downto 0) := (
    0 => '1',
    1 => '0',
    others => '0'
  );
begin
  a <= clk;
  bb <= reset_n;
  ccc <= data_in;

  q <= a;
  u0 : entity work.al
    generic map (
      WIDTH => 8,
      DEPTH_LOG2 => 4,
      G => true,
      LONGER_NAME_AFTER_BLANK => 1
    )
    port map (
      clk => clk,
      reset_n => reset_n,
      data_in => ccc,
      q => open
    );
  process (clk) is
    variable x : integer;
    variable yy : integer;
  begin
    x := 1;
    yy := 2;
  end process;
end architecture;
