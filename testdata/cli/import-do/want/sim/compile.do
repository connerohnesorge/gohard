# Compile the counter for ModelSim.
vlib work
vlib mylib
vmap mylib mylib
vcom -2008 -work mylib ../src/pkg.vhd
vcom -2008 ../src/top.vhd ../tb/tb_top.vhd
vsim -c work.tb_top
run -all
quit
