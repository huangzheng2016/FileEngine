package pathutil

import "testing"

func TestValidateFSPath(t *testing.T) {
	valid := []string{
		"", // empty clears a target
		"/organized/photos",
		"organized/photos",
		"/a/b/c.txt",
		"/中文 分类/相册 2024",
		"/a.b/c-d_e(f)",
		"relative/dir",
	}
	for _, p := range valid {
		if err := ValidateFSPath(p); err != nil {
			t.Fatalf("ValidateFSPath(%q): got error %v, want nil", p, err)
		}
	}
	invalid := []string{
		"/", ".", "..", "../x", "/..", "/a/../b", "a/../../b", "//../x", "a/b/../../../c", "a\x00b",
	}
	for _, p := range invalid {
		if err := ValidateFSPath(p); err == nil {
			t.Fatalf("ValidateFSPath(%q): got nil, want error", p)
		}
	}
}

func TestRootRelative(t *testing.T) {
	cases := map[string]string{
		"/photos/a.jpg":   "photos/a.jpg",
		"photos/a.jpg":    "photos/a.jpg",
		"//photos//a.jpg": "photos/a.jpg",
		"/a/./b":          "a/b",
	}
	for in, want := range cases {
		got, err := RootRelative(in)
		if err != nil || got != want {
			t.Fatalf("RootRelative(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, bad := range []string{"", "/", "../x", "/a/../../etc"} {
		if _, err := RootRelative(bad); err == nil {
			t.Fatalf("RootRelative(%q): got nil error", bad)
		}
	}
}
