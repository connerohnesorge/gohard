-- crypt_core: synthetic opaque IP for the standin_model corpus design.
-- The entity is plain text so its ports are known; the architecture is
-- an IEEE 1735-style protected envelope with dummy content. The key and
-- data blocks are sha256-derived filler, not ciphertext: nothing can
-- decrypt them. GoHard treats the unit as opaque and simulates the
-- stand-in models/crypt_core_model.vhd instead (simulation.models).
library ieee;
use ieee.std_logic_1164.all;

entity crypt_core is
  port (
    clk   : in  std_logic;
    rst   : in  std_logic;
    din   : in  std_logic_vector(7 downto 0);
    dout  : out std_logic_vector(7 downto 0);
    valid : out std_logic
  );
end entity crypt_core;

`protect begin_protected
`protect version = 1
`protect encrypt_agent = "gohard-corpus"
`protect encrypt_agent_info = "synthetic placeholder, not real ciphertext"
`protect key_keyowner = "gohard-corpus"
`protect key_keyname = "dummy-key-1"
`protect key_method = "rsa"
`protect encoding = (enctype = "base64", line_length = 64, bytes = 128)
`protect key_block
akncHAMUsuUMn15OSwQPqnX+x6m2/n/RA3iVIToRmh7Vmy5Oy9vauBaAkBVVzMtn
jrCPEdbplBI2/QGUtlpS5De3xbzthu/L2epC0fESA/MvAa7XHToTNe/sRZjfnhZD
gbamEcdvcgXcUjq3RoU58cZLAy6WQeXdo2U5iFdVVjU=
`protect data_method = "aes128-cbc"
`protect encoding = (enctype = "base64", line_length = 64, bytes = 384)
`protect data_block
Q8ueau7njBWuYPX+e043bok8rRENC/2rsTcd8IY/IRAKRErufGGw+/MDLRcmq845
Boq+fB09Fu/PLEfUQCDTIzz8wxwFhQNSWYw56IMSSYL4159pxEpuOPAnab2eimeG
RMHYVn7lV9UoNRnDa1xd4YFBEshpZGlYQH2c5wc2tysFysW4aLpHcTY2QjXC2OaV
ZHfrt6srmZBm3Y+TQBTLBvyHJ0V2Ya/9rYATU82/QZ4aTNxhSta6kiBklTNHnup6
h/vNgniTLzXRuwYq+sr5W+ZWlnRpe1ZsM+pvNsQofisqvorW9QB9ZV50Z/+a7d6X
24gcH85GswanTtnUXeF8vqtoom+q9jUvHLeluwMx0yEOJtoL4KHiEGPMYAwPfZkH
0brE7+bcIRRd/Md154KS+9IzB1IfQe3cBJ3ZR6PgSBcJyg19dD0G8NTcPrMj0XYE
/a0WLOi5UZXvWATkJAcsSWBvoPHcI6th8rKFgj9a1097W1fzBdnYIa+RUvYRynTA
`protect end_protected
