package gen

import (
	"testing"
	"time"
)

// Tool tests are exempt from every rule.
func TestFree(t *testing.T) {
	time.Sleep(0)
	for k := range map[int]int{} {
		t.Log(k)
	}
	go t.Log()
}
