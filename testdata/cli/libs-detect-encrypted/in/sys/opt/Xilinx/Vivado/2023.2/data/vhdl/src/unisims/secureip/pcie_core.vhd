-- fixture: an IEEE 1735 envelope written for the gohard libs detect
-- fixtures (#1); the blocks are filler, not vendor text.
entity pcie_core is
  port (clk : in bit);
end entity pcie_core;

`protect begin_protected
`protect version = 1
`protect encrypt_agent = "gohard-fixture"
`protect key_keyowner = "gohard-fixture"
`protect key_method = "rsa"
`protect key_block
bm90IGEgcmVhbCBrZXkgYmxvY2s=
`protect data_method = "aes128-cbc"
`protect data_block
bm90IHJlYWwgY2lwaGVydGV4dCwgb25seSBmaWxsZXI=
`protect end_protected
