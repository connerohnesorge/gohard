//go:build unix

package main

import (
	"os"
	"runtime"
	"syscall"
)

// peakRSS returns the peak resident set size of a finished process in
// MiB: getrusage's ru_maxrss, in KiB on Linux and in bytes on macOS.
func peakRSS(ps *os.ProcessState) float64 {
	if ps == nil {
		return 0
	}
	ru, ok := ps.SysUsage().(*syscall.Rusage)
	if !ok || ru == nil {
		return 0
	}
	if runtime.GOOS == "darwin" {
		return float64(ru.Maxrss) / (1 << 20)
	}
	return float64(ru.Maxrss) / (1 << 10)
}
