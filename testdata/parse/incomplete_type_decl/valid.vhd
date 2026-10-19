package p is
  type node;
  type node_ptr is access node;
  type node is record
    next_node : node_ptr;
  end record;
end;
