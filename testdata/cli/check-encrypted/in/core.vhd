entity core is
  port (
    d : in  bit_vector(7 downto 0);
    q : out bit_vector(7 downto 0)
  );
end entity core;

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
