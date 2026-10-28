// Package jsonrpc2 is the one package exempt from the go and select
// rules: it runs the stdio reader and writer goroutines. Every other
// rule still applies.
package jsonrpc2

import (
	"os"
	"time"
)

func Pump(in <-chan int, out chan<- int, done <-chan struct{}) {
	go func() { // allowed: the stdio reader
		for v := range in {
			select { // allowed: deliver or stop
			case out <- v:
			case <-done:
				return
			}
		}
	}()
	_ = time.Now()                 // want `use of time.Now`
	_, _ = os.ReadFile("x")        // want `use of os.ReadFile`
	_ = os.Stdin                   // want `use of os.Stdin`
	for k := range map[int]int{} { // want `range over a map`
		out <- k
	}
}
