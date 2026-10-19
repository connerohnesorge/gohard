package p is
  type fifo is protected
    generic (depth : natural := 8);
    procedure push (x : integer);
  end protected fifo;
end;
