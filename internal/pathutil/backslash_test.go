package pathutil

import "testing"

func TestValidateFSPathRejectsBackslash(t *testing.T) {
	for _, p := range []string{`x\..\..\secret`, `a\b`, `/a/b\c`} {
		if err := ValidateFSPath(p); err == nil {
			t.Fatalf("ValidateFSPath(%q): got nil, want error", p)
		}
	}
}
