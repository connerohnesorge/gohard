// Command gohard is the GoHard VHDL language server, checker, formatter
// and simulator. It is a thin shell: it builds the real Env, maps
// signals to the #20 exit codes, and hands the command line to cli.Run.
package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"os/signal"
	"runtime/debug"
	"syscall"
	"time"

	"github.com/connerohnesorge/gohard/internal/cli"
	"github.com/connerohnesorge/gohard/internal/env"
	"github.com/connerohnesorge/gohard/internal/env/realenv"
)

// grace is how long a run may take to wind down after the first signal
// before the shell exits anyway (a wall-time guard, allowed only here,
// P7).
const grace = 2 * time.Second

func main() {
	ctx, cancel := context.WithCancelCause(context.Background())
	sigs := make(chan os.Signal, 2)
	signal.Notify(sigs, os.Interrupt, syscall.SIGTERM)
	// A closed stdout (| head) must not kill the process with SIGPIPE
	// (exit 141 is not a #20 code): with SIGPIPE caught, writes fail
	// with EPIPE and cli.Run ignores the failed write, so the run exits
	// quietly with its own code. The channel is never read; subprocesses
	// still start with the default SIGPIPE action.
	signal.Notify(make(chan os.Signal, 1), syscall.SIGPIPE)
	go func() {
		intr := interrupt(<-sigs)
		cancel(intr)
		select {
		case s := <-sigs: // a second signal exits at once
			os.Exit(interrupt(s).Exit)
		case <-time.After(grace):
			os.Exit(intr.Exit)
		}
	}()
	bi, _ := debug.ReadBuildInfo()
	cli.SetBuildInfo(bi)
	// A hash of the executable's content is part of the analysis cache
	// key (#37), so a rebuilt binary never reads what another build
	// wrote, even with the same version, commit, size and time (nix
	// store and snapshot builds). It is computed only when a command
	// opens the cache.
	cli.SetBinary(func() string {
		exe, err := os.Executable()
		if err != nil {
			return ""
		}
		return binaryIdentity(exe)
	})
	os.Exit(cli.Run(ctx, realenv.New(realenv.Options{}), os.Args[1:]))
}

// binaryIdentity is the SHA-256 of the file at path, in hex: the
// identity of a gohard build for the analysis cache key. A file that
// cannot be read gives "unreadable", which turns the cache off (the
// identity of the code is unknown).
func binaryIdentity(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return "unreadable"
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "unreadable"
	}
	return hex.EncodeToString(h.Sum(nil))
}

func interrupt(s os.Signal) env.Interrupt {
	if s == syscall.SIGTERM {
		return env.SIGTERM
	}
	return env.SIGINT
}
