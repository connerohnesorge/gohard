#!/usr/bin/env bash
# Records GHDL synthesis results for the examples of
# docs/synth-subset.md (#67). Run from anywhere inside the devShell:
#
#   nix develop -c bash testdata/synth/record.sh
#
# Each <example>/ghdl.txt holds the exact command, the GHDL version,
# the exit status, stderr and stdout. neorv32/<config>.txt holds the
# same for NEORV32's rtl/file_list_core.f in four configurations;
# neorv32/netlist.txt and neorv32/scan.txt are the netlist and text
# checks behind the NEORV32 verdicts. Commands run inside the example
# (or testdata/neorv32) directory, so recorded paths are relative.
set -u
here=$(cd "$(dirname "$0")" && pwd)
version=$(ghdl --version | head -1)

# record OUT DIR ARGS...: run "ghdl --synth --std=08 ARGS" in DIR.
record() {
	local out=$1 dir=$2
	shift 2
	local o e rc
	o=$(mktemp)
	e=$(mktemp)
	(cd "$dir" && ghdl --synth --std=08 "$@" >"$o" 2>"$e")
	rc=$?
	{
		echo "command: ghdl --synth --std=08 $*"
		echo "version: $version"
		echo "exit: $rc"
		echo "--- stderr"
		# GHDL's crash report prints the working directory.
		sed "s#^$dir/*\$#<example directory>#" "$e"
		echo "--- stdout"
		cat "$o"
	} >"$out"
	rm -f "$o" "$e"
}

for d in "$here"/*/; do
	d=${d%/}
	[ -f "$d/top.vhd" ] || continue
	record "$d/ghdl.txt" "$d" top.vhd -e top
	echo "$(basename "$d"): $(sed -n 3p "$d/ghdl.txt")"
done

neo=$here/../neorv32
files=()
while IFS= read -r f; do
	f=${f%$'\r'}
	[ -n "$f" ] && files+=("${f#\$NEORV32_HOME/}")
done <"$neo/rtl/file_list_core.f"

mkdir -p "$here/neorv32"
# neorv32 NAME ARGS...: synthesize rtl/file_list_core.f into neorv32/NAME.txt.
neorv32() {
	local name=$1
	shift
	record "$here/neorv32/$name.txt" "$neo" \
		--work=neorv32 --out=none "$@" "${files[@]}" -e neorv32_top
	echo "neorv32 $name: $(sed -n 3p "$here/neorv32/$name.txt")"
	# Count netlist shapes that the accepted-but-wrong rules produce:
	# an edge used as data (edge-else), the dual-edge cell
	# (two-edges) and an initialized register (init-as-reset).
	local net
	net=$(mktemp)
	(cd "$neo" && ghdl --synth --std=08 --work=neorv32 --out=vhdl \
		"$@" "${files[@]}" -e neorv32_top >"$net" 2>/dev/null)
	{
		echo "$name:"
		echo "  edge-as-data $(grep -cE 'when (rising|falling)_edge' "$net")"
		echo "  gate_mdff    $(grep -c 'gate_mdff' "$net")"
		echo "  init-signal  $(grep -cE '^ *signal [^;]*:=' "$net")"
	} >>"$here/neorv32/netlist.txt"
	rm -f "$net"
}
echo "netlist shapes of NEORV32 (ghdl --synth --out=vhdl, see record.sh)" \
	>"$here/neorv32/netlist.txt"

# The default generics.
neorv32 default

# Every boolean feature generic on, except RISCV_ISA_E (fewer
# registers) and RISCV_ISA_Zmmul (a subset of M), plus a few counts.
gen=()
for g in TRACE_PORT_EN DUAL_CORE_EN OCD_EN OCD_AUTHENTICATION \
	RISCV_ISA_C RISCV_ISA_M RISCV_ISA_U RISCV_ISA_Zaamo \
	RISCV_ISA_Zalrsc RISCV_ISA_Zba RISCV_ISA_Zbb RISCV_ISA_Zbc \
	RISCV_ISA_Zbkb RISCV_ISA_Zbkc RISCV_ISA_Zbkx RISCV_ISA_Zbs \
	RISCV_ISA_Zcb RISCV_ISA_Zcmop RISCV_ISA_Zfinx RISCV_ISA_Zibi \
	RISCV_ISA_Zicntr RISCV_ISA_Zicond RISCV_ISA_Zihpm RISCV_ISA_Zimop \
	RISCV_ISA_Zknd RISCV_ISA_Zkne RISCV_ISA_Zknh RISCV_ISA_Zksed \
	RISCV_ISA_Zksh RISCV_ISA_Smcntrpmf RISCV_ISA_Xcfu CPU_CONSTT_BR_EN \
	CPU_FAST_MUL_EN CPU_FAST_SHIFT_EN PMP_TOR_MODE_EN PMP_NAP_MODE_EN \
	IMEM_EN IMEM_OUTREG_EN DMEM_EN DMEM_OUTREG_EN ICACHE_EN DCACHE_EN \
	CACHE_BURSTS_EN SMC_EN XBUS_EN XBUS_REGSTAGE_EN IO_GPIO_DIR_EN \
	IO_CLINT_EN IO_UART0_EN IO_UART1_EN IO_SPI_EN IO_SDI_EN IO_TWI_EN \
	IO_TWD_EN IO_WDT_EN IO_TRNG_EN IO_CFS_EN IO_NEOLED_EN \
	IO_ONEWIRE_EN IO_DMA_EN IO_SLINK_EN IO_TRACER_EN \
	IO_TRACER_SIMLOG_EN; do
	gen+=("-g$g=true")
done
gen+=(-gCLOCK_FREQUENCY=100000000 -gOCD_NUM_HW_TRIGGERS=4
	-gPMP_NUM_REGIONS=4 -gHPM_NUM_CNTS=4 -gIO_GPIO_NUM=8 -gIO_PWM_NUM=4
	-gIO_GPTMR_NUM=2)
neorv32 full "${gen[@]}"

# The latch register file (rule latch).
neorv32 rf-latch -gCPU_RF_ARCH_SEL=3

# Zbb with the serial shifter, whose control process has an
# incomplete sensitivity list (left out of the rule list).
neorv32 serial-shift -gRISCV_ISA_Zbb=true

# A text scan for the constructs of the rule list, as a baseline for
# the NEORV32 verdicts: comments removed, and synthesis-off regions
# removed or kept. A hit is a line that needs a look, not a finding
# (a signal named "stop" counts for sim-control).
strip() {
	awk -v keep="$1" 'BEGIN { off = 0 }
	{ l = tolower($0) }
	l ~ /--[ \t]*(pragma|synopsys|synthesis)[ \t]+(synthesis|translate)[ _]off/ ||
	l ~ /--[ \t]*rtl_synthesis[ \t]+off/ { off = 1; next }
	l ~ /--[ \t]*(pragma|synopsys|synthesis)[ \t]+(synthesis|translate)[ _]on/ ||
	l ~ /--[ \t]*rtl_synthesis[ \t]+on/ { off = 0; next }
	{ if (!off || keep) { sub(/--.*/, ""); print } }' "${@:2}"
}
scan() {
	local keep=$1
	shift
	local rule pat
	while read -r rule pat; do
		printf '  %-13s %s\n' "$rule" \
			"$(strip "$keep" "$@" | grep -ciE "$pat")"
	done <<'PATTERNS'
wait \bwait\b
after-delay \b(after|transport|inertial)\b
time-now \b(now|time)\b
sim-control std\.env|\b(finish|stop)\b
access-type \b(access|new|deallocate)\b
file-io \bfile\b|textio
real-signal \breal\b
shared-var \b(shared|protected)\b
init-signal ^\s*signal\s[^;]*:=
guarded \b(guarded|register|bus|disconnect)\b
history-attr '(last_value|last_event|last_active|delayed|stable|quiet|transaction|active)\b
while-loop \bwhile\b
PATTERNS
}
(
	cd "$neo" || exit 1
	echo "text scan of NEORV32 (lines with a hit; see record.sh)"
	echo "rtl/file_list_core.f, synthesis-off regions removed:"
	scan "" "${files[@]}"
	echo "rtl/file_list_core.f, synthesis-off regions kept:"
	scan 1 "${files[@]}"
	echo "sim/*.vhd, synthesis-off regions kept:"
	scan 1 sim/*.vhd
) >"$here/neorv32/scan.txt"
cat "$here/neorv32/scan.txt"
