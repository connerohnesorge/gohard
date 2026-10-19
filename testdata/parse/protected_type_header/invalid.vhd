package p is
  type fifo is protected
    generic depth : natural;
  end protected fifo;
end;
