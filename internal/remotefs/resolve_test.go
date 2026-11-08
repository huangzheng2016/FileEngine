package remotefs

import (
	"strings"
	"testing"
)

func TestConfineSlashPath(t *testing.T) {
	ok := []struct{ resolved, base string }{
		{"/data/x", "/data"},
		{"/data", "/data"},        // the base itself
		{"/data/a/b/c", "/data/"}, // trailing slash on base
		{"data/x", "data"},        // relative forms (NFS subPath shape)
		{"/anything/at/all", ""},  // empty base confines nothing
		{"/etc/passwd", ""},       // ditto
		{"/sub", "/sub"},
	}
	for _, tc := range ok {
		if err := confineSlashPath(tc.resolved, tc.base); err != nil {
			t.Fatalf("confineSlashPath(%q, %q): got %v, want nil", tc.resolved, tc.base, err)
		}
	}

	bad := []struct{ resolved, base string }{
		{"/etc/passwd", "/data"},  // absolute escape
		{"/data/../etc", "/data"}, // '..' escape (pre-Clean form)
		{"/data2/x", "/data"},     // sibling prefix must not bleed
		{"/", "/data"},            // root is outside a non-empty base
		{"etc", "data"},           // relative escape (NFS shape)
		{"/sub/../other", "/sub"},
	}
	for _, tc := range bad {
		if err := confineSlashPath(tc.resolved, tc.base); err == nil {
			t.Fatalf("confineSlashPath(%q, %q): got nil, want rejection", tc.resolved, tc.base)
		}
	}
}

// resolve is pure, so the backends are constructed bare with only a
// basePath.
func TestBackendResolveConfinesToBasePath(t *testing.T) {
	type resolver interface {
		resolve(p string) (string, error)
	}
	backends := map[string]resolver{
		"sftp": &SFTPFS{basePath: "/vol1/data"},
		"ftp":  &FTPFS{basePath: "/srv/ftp"},
		"nfs":  &NFSFS{basePath: "exports/data"}, // subPath inside the export
		"smb":  &SMBFS{basePath: "/share/sub"},   // subPath inside the share
	}

	inBase := map[string][]string{
		"sftp": {"incoming/a.txt", "/vol1/data/incoming/a.txt", "."},
		"ftp":  {"incoming/a.txt", "/srv/ftp/incoming/a.txt", "."},
		"nfs":  {"incoming/a.txt", "/exports/data/incoming/a.txt", "."},
		"smb":  {"incoming/a.txt", "/share/sub/incoming/a.txt", "."},
	}
	escapes := map[string][]string{
		"sftp": {"/etc/passwd", "../outside", "/vol1/data2/x", "/vol1", "/"},
		"ftp":  {"/etc/passwd", "../outside", "/srv/ftp2/x", "/srv", "/"},
		"nfs":  {"/etc/passwd", "../outside", "/exports/data2/x", "/exports", "/"},
		"smb":  {"/etc/passwd", "../outside", "/share/sub2/x", "/share", "/"},
	}

	for name, fs := range backends {
		for _, p := range inBase[name] {
			if _, err := fs.resolve(p); err != nil {
				t.Fatalf("%s.resolve(%q): got %v, want in-base", name, p, err)
			}
		}
		for _, p := range escapes[name] {
			if _, err := fs.resolve(p); err == nil {
				t.Fatalf("%s.resolve(%q): got nil, want escape rejection", name, p)
			} else if !strings.Contains(err.Error(), "escapes base path") {
				t.Fatalf("%s.resolve(%q): error %v lacks 'escapes base path'", name, p, err)
			}
		}
	}
}

func TestBackendResolveEmptyBaseAllowsAll(t *testing.T) {
	sftp := &SFTPFS{}
	if got, err := sftp.resolve("/etc/passwd"); err != nil || got != "/etc/passwd" {
		t.Fatalf("sftp empty-base resolve: got %q, %v", got, err)
	}
	nfs := &NFSFS{}
	if _, err := nfs.resolve("any/where"); err != nil {
		t.Fatalf("nfs empty-base resolve: %v", err)
	}
	smb := &SMBFS{}
	if got, err := smb.resolve("/"); err != nil || got != "." {
		t.Fatalf("smb empty-base root resolve: got %q, %v", got, err)
	}
}
