package filesystem

import (
	"context"
	"errors"
	"strings"
	"testing"

	"FileEngine/biz/dal/db/dbtest"
	"FileEngine/biz/dal/db/model"
	"FileEngine/biz/dal/db/query"
	"FileEngine/biz/errs"
	"FileEngine/internal/secret"
)

func setup(t *testing.T) (*Service, *query.Query) {
	t.Helper()
	if err := secret.SetKey(make([]byte, 32)); err != nil {
		t.Fatal(err)
	}
	q := dbtest.New(t)
	return NewService(q), q
}

func ptr(s string) *string { return &s }

func ftp(name string) SaveInput {
	return SaveInput{Name: name, Conn: Conn{Protocol: "ftp", Host: "192.168.1.10", Port: 21, Username: "u", BasePath: "/data", Password: ptr("pw")}}
}

func wantErr(t *testing.T, err error, status int, msg string) {
	t.Helper()
	var e *errs.Error
	if !errors.As(err, &e) || e.Status != status || !strings.Contains(e.Msg, msg) {
		t.Fatalf("err = %v, want %d containing %q", err, status, msg)
	}
}

func TestCreateAddsDefaultCategoryAndEncryptsSecrets(t *testing.T) {
	ctx := context.Background()
	s, q := setup(t)
	fs, err := s.Create(ctx, ftp("nas"))
	if err != nil {
		t.Fatal(err)
	}
	cats, _ := q.Category.WithContext(ctx).Where(q.Category.FilesystemID.Eq(fs.ID)).Find()
	if len(cats) != 1 || cats[0].Path != "/未分类" {
		t.Fatalf("default category: %+v", cats)
	}
	var raw string
	if err := q.Filesystem.WithContext(ctx).UnderlyingDB().Raw("SELECT password FROM filesystems WHERE id = ?", fs.ID).Scan(&raw).Error; err != nil {
		t.Fatal(err)
	}
	if raw == "" || raw == "pw" {
		t.Fatalf("password stored as %q", raw)
	}
	wantErr(t, mustFail(s.Create(ctx, ftp("nas"))), 409, "名称已存在")
}

func mustFail(_ *model.Filesystem, err error) error { return err }

func TestUpdateSecretRules(t *testing.T) {
	ctx := context.Background()
	s, _ := setup(t)
	fs, err := s.Create(ctx, ftp("nas"))
	if err != nil {
		t.Fatal(err)
	}

	keep := ftp("nas2")
	keep.ID, keep.Password = fs.ID, nil
	got, err := s.Update(ctx, keep)
	if err != nil || got.Password != "pw" || got.Name != "nas2" {
		t.Fatalf("absent password must keep the stored one: %+v %v", got, err)
	}

	moved := keep
	moved.Host = "192.168.1.99"
	wantErr(t, mustFail(s.Update(ctx, moved)), 400, "重新输入密码")

	moved.Password = ptr("new")
	got, err = s.Update(ctx, moved)
	if err != nil || got.Password != "new" || got.Host != "192.168.1.99" {
		t.Fatalf("retyped password: %+v %v", got, err)
	}

	cleared := moved
	cleared.Password = ptr("")
	got, err = s.Update(ctx, cleared)
	if err != nil || got.Password != "" {
		t.Fatalf("empty clears: %+v %v", got, err)
	}
}

func TestGetMissingIsNotFound(t *testing.T) {
	s, _ := setup(t)
	_, err := s.Get(context.Background(), 99)
	wantErr(t, err, 404, "存储不存在")
}

func TestDeleteRefusedWhileInUse(t *testing.T) {
	ctx := context.Background()
	s, q := setup(t)
	fs, _ := s.Create(ctx, ftp("nas"))
	for range 2 {
		if err := q.ScanSession.WithContext(ctx).Create(&model.ScanSession{FilesystemID: fs.ID}); err != nil {
			t.Fatal(err)
		}
	}
	wantErr(t, s.Delete(ctx, fs.ID), 409, "有 2 个整理任务在用这个存储，先删除这些任务")
	if _, err := s.Get(ctx, fs.ID); err != nil {
		t.Fatalf("refused delete removed the row: %v", err)
	}
	if _, err := q.ScanSession.WithContext(ctx).Where(q.ScanSession.FilesystemID.Eq(fs.ID)).Delete(); err != nil {
		t.Fatal(err)
	}
	if err := s.Delete(ctx, fs.ID); err != nil {
		t.Fatal(err)
	}
	if n, _ := q.Category.WithContext(ctx).Count(); n != 0 {
		t.Fatalf("categories left behind: %d", n)
	}
	if err := s.Delete(ctx, fs.ID); err != nil {
		t.Fatalf("deleting a missing storage must succeed: %v", err)
	}
}

func TestValidation(t *testing.T) {
	s, _ := setup(t)
	ctx := context.Background()
	cases := map[string]func(*SaveInput){
		"unknown protocol": func(in *SaveInput) { in.Protocol = "webdav" },
		"no host":          func(in *SaveInput) { in.Host = " " },
		"bad port":         func(in *SaveInput) { in.Port = 70000 },
		"no base path":     func(in *SaveInput) { in.BasePath = "" },
		"no name":          func(in *SaveInput) { in.Name = "" },
	}
	for name, mut := range cases {
		t.Run(name, func(t *testing.T) {
			in := ftp("x")
			mut(&in)
			var e *errs.Error
			if _, err := s.Create(ctx, in); !errors.As(err, &e) || e.Status != 400 {
				t.Fatalf("err = %v", err)
			}
		})
	}
}
