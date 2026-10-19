// Package tool is exempt: nothing is reported.
package tool

import (
	crand "crypto/rand"
	"fmt"
	"io/ioutil"
	"log"
	"log/slog"
	"math/rand"
	randv2 "math/rand/v2"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/connerohnesorge/gohard/internal/env/realenv"
)

var (
	_ = crand.Reader
	_ = ioutil.ReadFile
	_ = rand.Int
	_ = randv2.Int
	_ = net.Dial
	_ = http.Get
	_ = netip.Addr{}
	_ = url.Parse
	_ = exec.Command
	_ = signal.Notify
	_ = syscall.Getpid
	_ = realenv.Name
)

func clock() {
	_ = time.Now()
	time.Sleep(1)
	<-time.After(1)
	time.AfterFunc(1, clock)
	_ = time.NewTimer(1)
	_ = time.NewTicker(1)
	_ = time.Tick(1)
	_ = time.Since(time.Time{})
	_ = time.Until(time.Time{})
	_, _ = time.LoadLocation("UTC")
	_ = time.Local
	now := time.Now
	_ = now
	// Pure time values are fine.
	_ = time.Duration(3) * time.Second
	_ = time.Unix(0, 0).UTC()
	_ = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
}

func world() {
	_ = os.Getenv("HOME")
	_, _ = os.LookupEnv("HOME")
	_ = os.Environ()
	_ = os.ExpandEnv("$HOME")
	_, _ = os.Open("f")
	_, _ = os.Create("f")
	_, _ = os.ReadFile("f")
	_ = os.WriteFile("f", nil, 0)
	_ = os.Remove("f")
	_ = os.RemoveAll("f")
	_ = os.Rename("a", "b")
	_ = os.Mkdir("d", 0)
	_ = os.MkdirAll("d", 0)
	_, _ = os.MkdirTemp("", "")
	_, _ = os.Stat("f")
	_, _ = os.Lstat("f")
	_, _ = os.ReadDir("d")
	_ = os.DirFS("d")
	_ = os.TempDir()
	_ = os.Stdin
	_ = os.Stdout
	_ = os.Stderr
	_ = os.Args
	_, _ = os.Getwd()
	_, _ = os.Hostname()
	os.Exit(1)
	_, _ = filepath.Abs("x")
	_ = filepath.WalkDir(".", nil)
	_, _ = filepath.Glob("*")
	fmt.Println("x")
	fmt.Printf("x")
	_, _ = fmt.Scanln()
	log.Printf("x")
	log.Fatal("x")
	slog.Info("x")
}

func pure(err error, fi, fj os.FileInfo, f *os.File) {
	// Types, constants, Err* values and pure helpers of os are fine.
	_ = os.IsNotExist(err) || os.IsExist(err) || os.IsPermission(err)
	_ = err == os.ErrNotExist
	_ = os.ModeDir | os.ModePerm
	_ = os.O_RDONLY | os.O_CREATE
	_ = os.PathSeparator
	_ = os.SameFile(fi, fj)
	_ = os.Expand("$x", func(string) string { return "" })
	_ = f.Close() // methods on values handed in through env.FS
	_ = filepath.Join("a", "b")
	_ = fmt.Sprintf("%d", 1)
	_, _ = fmt.Fprintf(nil, "x")
	_ = log.New(nil, "", 0)
	_ = slog.New(nil)
}

func concurrency(c chan int) {
	go clock()
	select {
	case <-c:
	default:
	}
}
