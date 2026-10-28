package core_test

import (
	"os"
	"time"
)

var (
	_ = os.Getenv
	_ = time.Now // want `use of time.Now`
)
