package library

import "os"

func Read(name string) { _, _ = os.ReadFile(name) }
