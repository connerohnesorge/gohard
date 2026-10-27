# Run from the project folder: vsim -do scripts/compile.do
vlib work
vlib mylib
vcom -2008 -work mylib lib/*/p?g.vhd
vcom -2008 src/*.vhd
vsim -c work.top
