-- file header
-- indented header line
entity cm is
  -- before the port clause
  port (
    -- inside the port list
    a : in bit; -- trailing comment keeps its distance
    -- deeply indented comment
    b : out bit
    -- just before the close
  );
  -- before end
end;
architecture a of cm is
begin
  process
    -- in the declarative part
  begin
    -- in the body
    wait;
    -- before end process
  end process;
end;
