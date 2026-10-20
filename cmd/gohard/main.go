// Command gohard is the GoHard VHDL language server, checker, formatter
// and simulator. It is a thin shell: it builds the real Env, maps
// signals to the #20 exit codes, and hands the command line to cli.Run.
package main

import (
	"context"
	"os"
	"os/signal"
	"runtime/debug"
	"strings"
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
	cli.Version = version(cli.Version)
	os.Exit(cli.Run(ctx, realenv.New(realenv.Options{}), os.Args[1:]))
}

func interrupt(s os.Signal) env.Interrupt {
	if s == syscall.SIGTERM {
		return env.SIGTERM
	}
	return env.SIGINT
}

// version returns the release version: the -ldflags value when set (the
// goreleaser and Nix builds), else the module version recorded by
// `go install ...@vX.Y.Z` without its leading "v", else def.
func version(def string) string {
	if def != "0.0.0-dev" {
		return def
	}
	bi, ok := debug.ReadBuildInfo()
	if !ok || bi.Main.Version == "" || bi.Main.Version == "(devel)" {
		return def
	}
	return strings.TrimPrefix(bi.Main.Version, "v")
}
