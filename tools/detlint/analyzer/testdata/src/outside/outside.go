// Package outside is outside the GoHard module: every rule applies.
package outside

import (
	crand "crypto/rand" // want `import of crypto/rand`
	"fmt"
	"io/ioutil" // want `import of io/ioutil`
	"log"
	"log/slog"
	"math/rand"           // want `import of math/rand: take randomness`
	randv2 "math/rand/v2" // want `import of math/rand/v2`
	"net"                 // want `import of net: the network`
	"net/http"            // want `import of net/http`
	"net/netip"
	"net/url"
	"os"
	"os/exec"   // want `import of os/exec: use the Executor`
	"os/signal" // want `import of os/signal`
	"path/filepath"
	"syscall" // want `import of syscall`
	"time"
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
)

func clock() {
	_ = time.Now()                  // want `use of time.Now: take time from`
	time.Sleep(1)                   // want `use of time.Sleep`
	<-time.After(1)                 // want `use of time.After`
	time.AfterFunc(1, clock)        // want `use of time.AfterFunc`
	_ = time.NewTimer(1)            // want `use of time.NewTimer`
	_ = time.NewTicker(1)           // want `use of time.NewTicker`
	_ = time.Tick(1)                // want `use of time.Tick`
	_ = time.Since(time.Time{})     // want `use of time.Since`
	_ = time.Until(time.Time{})     // want `use of time.Until`
	_, _ = time.LoadLocation("UTC") // want `use of time.LoadLocation`
	_ = time.Local                  // want `use of time.Local`
	now := time.Now                 // want `use of time.Now`
	_ = now
	// Pure time values are fine.
	_ = time.Duration(3) * time.Second
	_ = time.Unix(0, 0).UTC()
	_ = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
}

func world() {
	_ = os.Getenv("HOME")          // want `use of os.Getenv: read the`
	_, _ = os.LookupEnv("HOME")    // want `use of os.LookupEnv`
	_ = os.Environ()               // want `use of os.Environ`
	_ = os.ExpandEnv("$HOME")      // want `use of os.ExpandEnv`
	_, _ = os.Open("f")            // want `use of os.Open: reach files through`
	_, _ = os.Create("f")          // want `use of os.Create`
	_, _ = os.ReadFile("f")        // want `use of os.ReadFile`
	_ = os.WriteFile("f", nil, 0)  // want `use of os.WriteFile`
	_ = os.Remove("f")             // want `use of os.Remove`
	_ = os.RemoveAll("f")          // want `use of os.RemoveAll`
	_ = os.Rename("a", "b")        // want `use of os.Rename`
	_ = os.Mkdir("d", 0)           // want `use of os.Mkdir`
	_ = os.MkdirAll("d", 0)        // want `use of os.MkdirAll`
	_, _ = os.MkdirTemp("", "")    // want `use of os.MkdirTemp`
	_, _ = os.Stat("f")            // want `use of os.Stat`
	_, _ = os.Lstat("f")           // want `use of os.Lstat`
	_, _ = os.ReadDir("d")         // want `use of os.ReadDir`
	_ = os.DirFS("d")              // want `use of os.DirFS`
	_ = os.TempDir()               // want `use of os.TempDir`
	_ = os.Stdin                   // want `use of os.Stdin: write through the`
	_ = os.Stdout                  // want `use of os.Stdout`
	_ = os.Stderr                  // want `use of os.Stderr`
	_ = os.Args                    // want `use of os.Args: use the Executor`
	_, _ = os.Getwd()              // want `use of os.Getwd`
	_, _ = os.Hostname()           // want `use of os.Hostname`
	os.Exit(1)                     // want `use of os.Exit`
	_, _ = filepath.Abs("x")       // want `use of path/filepath.Abs`
	_ = filepath.WalkDir(".", nil) // want `use of path/filepath.WalkDir`
	_, _ = filepath.Glob("*")      // want `use of path/filepath.Glob`
	fmt.Println("x")               // want `use of fmt.Println: write through`
	fmt.Printf("x")                // want `use of fmt.Printf`
	_, _ = fmt.Scanln()            // want `use of fmt.Scanln`
	log.Printf("x")                // want `use of log.Printf`
	log.Fatal("x")                 // want `use of log.Fatal`
	slog.Info("x")                 // want `use of log/slog.Info`
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
	go clock() // want `go statement: run parallel work through the Jobs`
	select {   // want `select statement`
	case <-c:
	default:
	}
}
