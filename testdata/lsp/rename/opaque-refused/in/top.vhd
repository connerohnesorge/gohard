entity leaf is
end entity leaf;

architecture rtl of leaf is
begin
end architecture rtl;

entity top is
end entity top;

architecture rtl of top is
  signal s : bit;
  component leaf is
  end component leaf;
`protect begin_protected
`protect version = 1
`protect encrypt_agent = "gohard-fixture"
`protect key_keyowner = "gohard-fixture"
`protect key_method = "rsa"
`protect encoding = (enctype = "base64", line_length = 64, bytes = 16)
`protect key_block
bm90IGEgcmVhbCBrZXkgYmxvY2s=
`protect data_method = "aes128-cbc"
`protect encoding = (enctype = "base64", line_length = 64, bytes = 32)
`protect data_block
bm90IHJlYWwgY2lwaGVydGV4dCwgb25seSBmaWxsZXI=
`protect end_protected
begin
  u1 : component leaf;
  s <= '1';
end architecture rtl;
