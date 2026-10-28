-- race_order: a race on purpose (#63, #29). When go rises, eight
-- processes resume in the same simulation cycle and each appends its
-- number to a shared protected log. The LRM does not define the order
-- in which the processes of one cycle run (14.7.5.3), so the code they
-- leave depends on the order the simulator picks: 12345678 in
-- declaration order (GoHard at seed 0, NVC), 87654321 in reverse
-- (GHDL), another permutation at each other seed. publish drives the
-- code on order when sample rises, so the trace shows the race and
-- GoHard reports it as sim/race at every seed but 0.
entity race_order is
  port (
    go     : in  bit;
    sample : in  bit;
    order  : out natural
  );
end entity race_order;

architecture rtl of race_order is
  type log_t is protected
    procedure push (id : natural);
    impure function code return natural;
  end protected log_t;

  type log_t is protected body
    variable v : natural := 0;

    procedure push (id : natural) is
    begin
      v := v * 10 + id;
    end procedure push;

    impure function code return natural is
    begin
      return v;
    end function code;
  end protected body log_t;

  shared variable log : log_t;
begin
  writers : for i in 1 to 8 generate
    w : process is
    begin
      wait until go = '1';
      log.push(i);
      wait;
    end process w;
  end generate writers;

  -- Reads the log on sample, after the writers ran: it never resumes
  -- with them, so it is not part of the race.
  publish : process is
  begin
    wait until sample = '1';
    order <= log.code;
    wait;
  end process publish;
end architecture rtl;
