package task

import (
	"fmt"
	"os"
	"testing"

	"github.com/mbriggs/bunk/box"
)

// TestMain runs a box's helper when a box runs this binary as one (see
// box.Helper), instead of running the tests again.
func TestMain(m *testing.M) {
	if ok, err := box.Helper(os.Args[1:]); ok {
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}
