package jsonrpc2

import (
	"testing"
	"time"
)

func TestPipe(t *testing.T) {
	c := make(chan int)
	go func() { c <- 1 }() // allowed in its tests too
	select {               // allowed
	case <-c:
	case <-c:
	}
	time.Sleep(1) // want `use of time.Sleep`
}
