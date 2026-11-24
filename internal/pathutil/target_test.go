package pathutil

import "testing"

func TestValidateTargetPathRejectsControlChars(t *testing.T) {
	for _, p := range []string{"a\r\nDELE /x", "a\nb", "a\rb", "a\x00b", "a\x1bb", "a\x7fb", "a\tb"} {
		if err := ValidateTargetPath(p); err == nil {
			t.Fatalf("ValidateTargetPath(%q): got nil, want error", p)
		}
	}
	for _, p := range []string{"", "/中文 分类/a b.txt", "x/y"} {
		if err := ValidateTargetPath(p); err != nil {
			t.Fatalf("ValidateTargetPath(%q): %v", p, err)
		}
	}
	// Sources are not held to it: such names exist on POSIX filesystems.
	if err := ValidateFSPath("a\nb"); err != nil {
		t.Fatalf("ValidateFSPath must stay permissive for sources: %v", err)
	}
}
