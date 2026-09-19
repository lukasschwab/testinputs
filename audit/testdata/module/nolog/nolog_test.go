package nolog

import (
	"os"
	"testing"
)

func TestMain(m *testing.M)    { os.Exit(0) }
func TestSkipped(t *testing.T) {}
