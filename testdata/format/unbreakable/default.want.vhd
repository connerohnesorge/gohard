entity ub is
end;
architecture a of ub is
  constant VERY_LONG_CONSTANT_NAME_THAT_CANNOT_BE_BROKEN_AT_ALL_BECAUSE_ITS_ONE_WORD : integer := 0;
  constant TEXT : string := "an unbreakable string literal that by itself is far longer than eighty columns";
begin
end;
