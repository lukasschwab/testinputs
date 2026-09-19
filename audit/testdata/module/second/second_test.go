package second

import (
	"os"
	"testing"
)

func TestSecond(t *testing.T) { _, _ = os.ReadFile("second.txt") }
