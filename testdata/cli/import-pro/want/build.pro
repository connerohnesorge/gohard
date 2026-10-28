# Compile script in the OSVVM style. gohard import reads library,
# analyze and include, and takes the if branch its profile selects.
library mylib
analyze src/pkg.vhd
library work
if {$::osvvm::ToolName eq "XSIM"} {
  analyze src/top_xsim.vhd
} else {
  include src/rtl
}
analyze $::my::extraFile
if {[DirectoryExists optional]} {
  include optional
}
SetVHDLVersion 2008
analyze tb/tb_top.vhd
simulate tb_top
