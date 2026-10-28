package core

import (
	"math/rand" // want `import of math/rand`
	"os"
	"os/exec" // want `import of os/exec`
	"testing"
	"time"

	"github.com/connerohnesorge/gohard/v2/internal/env/realenv"
)

// Tests may use files, the environment, stdio and realenv; they may
// iterate maps in any order. Clock, rand, process, go and select stay
// banned, except a non-blocking poll of one channel.
func TestPolicy(t *testing.T) {
	_, _ = os.ReadFile("testdata/golden.txt")
	_ = os.WriteFile("testdata/golden.txt", nil, 0o644)
	_ = os.Getenv("UPDATE")
	_ = os.Stdout
	_ = realenv.Name
	for k, v := range map[string]int{"a": 1} {
		t.Log(k, v)
	}
	_ = rand.Int
	_ = exec.Command
	time.Sleep(1)  // want `use of time.Sleep`
	_ = time.Now() // want `use of time.Now`
	go t.Log()     // want `go statement`
	c := make(chan int, 1)
	select {
	case v := <-c:
		_ = v
	default:
	}
	select { // want `select statement`
	case <-c:
	}
	select { // want `select statement`
	case <-c:
	case c <- 1:
	default:
	}
	os.Exit(0) // want `use of os.Exit`
}
