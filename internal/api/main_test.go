package api

import (
	"bytes"
	"os"
	"testing"

	"FileEngine/internal/remotefs"
	"FileEngine/internal/secret"
)

// Tests use LOCAL filesystems under the temp dir and store secrets, so they
// allow the temp dir as a local root and install a throwaway key.
func TestMain(m *testing.M) {
	if err := remotefs.SetLocalPolicy(remotefs.LocalPolicy{Roots: []string{os.TempDir()}}); err != nil {
		panic(err)
	}
	if err := secret.SetKey(bytes.Repeat([]byte{42}, 32)); err != nil {
		panic(err)
	}
	os.Exit(m.Run())
}
