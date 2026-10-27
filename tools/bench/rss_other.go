//go:build !unix

package main

import "os"

// peakRSS is not measured on this OS.
func peakRSS(*os.ProcessState) float64 { return 0 }
