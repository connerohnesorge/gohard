entity elab_tb is
end entity;

architecture sim of elab_tb is
  function fib(n : natural) return natural is
  begin
    if n < 2 then
      return n;
    end if;
    return fib(n - 1) + fib(n - 2);
  end function;
  constant C : natural := fib(20);
  signal s : natural := C;
begin
end architecture;
