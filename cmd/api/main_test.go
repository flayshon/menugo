package main

import (
	"os"
	"testing"

	"menugo.flayshon.com/internal/testdb"
)

// TestMain lets testdb reuse databases between tests and drop them at the end.
func TestMain(m *testing.M) {
	os.Exit(testdb.Main(m))
}
