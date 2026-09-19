package partial

import (
	"flag"
	"os"
	"testing"
)

func TestMain(m *testing.M) {
	code := m.Run()
	_ = os.WriteFile(flag.Lookup("test.testlogfile").Value.String(), []byte("# test log\nopen unfinished"), 0600)
	os.Exit(code)
}
func TestPartial(t *testing.T) {}
