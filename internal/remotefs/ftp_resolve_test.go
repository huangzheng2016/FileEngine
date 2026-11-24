package remotefs

import "testing"

func TestFTPResolveRejectsControlChars(t *testing.T) {
	f := &FTPFS{basePath: "/srv/ftp"}
	for _, p := range []string{"a\r\nDELE /x", "a\nb", "a\rb", "a\x00b"} {
		if _, err := f.resolve(p); err == nil {
			t.Fatalf("resolve(%q): accepted", p)
		}
	}
	if _, err := f.resolve("ok/a b.txt"); err != nil {
		t.Fatal(err)
	}
}
