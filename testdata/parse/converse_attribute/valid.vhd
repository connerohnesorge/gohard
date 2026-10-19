-- test: kind=AttributeName,ModeViewIndication
entity e is
  port (m : view master; s : view master'converse);
end;
