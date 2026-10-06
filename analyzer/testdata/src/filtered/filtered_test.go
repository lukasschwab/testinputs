package filtered

import (
	"io/fs"
	"os"
	"testing"
)

func TestFiltered(t *testing.T) {
	var f fs.FS
	_, _ = fs.ReadFile(f, "unresolved")
	_, _ = os.ReadFile("disk") // want "TFS001: read os.ReadFile"
}
