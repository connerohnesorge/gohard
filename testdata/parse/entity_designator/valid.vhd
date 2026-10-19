package p is
  attribute foreign of f [integer return bit] : function is "c";
  attribute op of "and" : function is 1;
  attribute ch of '0' : literal is 2;
end;
