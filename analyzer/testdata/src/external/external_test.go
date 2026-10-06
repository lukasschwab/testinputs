package external_test

import (
	"dep"
	"os"
	"testing"
)

func TestImported(t *testing.T) {
	dep.Read("fixture") // want "TFS001: read os.ReadFile"
	dep.Read(dep.Join(t.TempDir()))
	_, _ = os.ReadFile("direct") // want "TFS001: read os.ReadFile"
}
