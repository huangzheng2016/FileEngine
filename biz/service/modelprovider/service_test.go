package modelprovider

import (
	"context"
	"errors"
	"strings"
	"testing"

	dal "FileEngine/biz/dal/db"
	"FileEngine/biz/dal/db/dbtest"
	"FileEngine/biz/dal/db/model"
	"FileEngine/biz/errs"
	"FileEngine/internal/secret"
)

func setup(t *testing.T) *Service {
	t.Helper()
	if err := secret.SetKey(make([]byte, 32)); err != nil {
		t.Fatal(err)
	}
	q := dbtest.New(t)
	return NewService(q, &dal.Reader{Query: q})
}

func ptr[T any](v T) *T { return &v }

func input(name string) SaveInput {
	return SaveInput{Name: name, Provider: "openai", Model: "m", BaseURL: "https://api.example.test/v1", MaxTokens: 100, APIKey: ptr("sk-1")}
}

func wantErr(t *testing.T, err error, status int, msg string) {
	t.Helper()
	var e *errs.Error
	if !errors.As(err, &e) || e.Status != status || !strings.Contains(e.Msg, msg) {
		t.Fatalf("err = %v, want %d containing %q", err, status, msg)
	}
}

func TestCreateDefaultsAndUpdateKeepsAbsentFields(t *testing.T) {
	ctx := context.Background()
	s := setup(t)
	m, err := s.Create(ctx, input("a"))
	if err != nil || m.ContextWindow != model.DefaultContextWindow || m.VisionEnabled {
		t.Fatalf("create: %+v %v", m, err)
	}
	in := input("a")
	in.ID, in.APIKey, in.VisionEnabled, in.ContextWindow = m.ID, nil, ptr(true), ptr(64000)
	got, err := s.Update(ctx, in)
	if err != nil || got.APIKey != "sk-1" || !got.VisionEnabled || got.ContextWindow != 64000 {
		t.Fatalf("update: %+v %v", got, err)
	}
	in.VisionEnabled, in.ContextWindow = nil, nil
	got, err = s.Update(ctx, in)
	if err != nil || !got.VisionEnabled || got.ContextWindow != 64000 {
		t.Fatalf("absent optional fields must keep the stored value: %+v %v", got, err)
	}
	in.VisionEnabled = ptr(false)
	if got, _ = s.Update(ctx, in); got.VisionEnabled {
		t.Fatal("false must be writable")
	}
}

func TestKeyNeverFollowsAChangedEndpoint(t *testing.T) {
	ctx := context.Background()
	s := setup(t)
	m, _ := s.Create(ctx, input("a"))
	in := input("a")
	in.ID, in.APIKey, in.BaseURL = m.ID, nil, "https://evil.example.test/v1"
	_, err := s.Update(ctx, in)
	wantErr(t, err, 400, "重新输入 API Key")
	_, err = s.resolveKey(ctx, m.ID, nil, "openai", "https://evil.example.test/v1")
	wantErr(t, err, 400, "重新输入 API Key")
	if k, err := s.resolveKey(ctx, m.ID, nil, "openai", m.BaseURL); err != nil || k != "sk-1" {
		t.Fatalf("same endpoint: %q %v", k, err)
	}
}

func TestDeleteRefusedWhileInUse(t *testing.T) {
	ctx := context.Background()
	s := setup(t)
	m, _ := s.Create(ctx, input("a"))
	ss := s.q.ScanSession
	_ = ss.WithContext(ctx).Create(&model.ScanSession{ModelProviderID: 99}, &model.ScanSession{ModelProviderID: 99, BulkModelProviderID: m.ID})
	wantErr(t, s.Delete(ctx, m.ID), 409, "有 1 个整理任务在用这个模型，先删除这些任务")
	if _, err := ss.WithContext(ctx).Where(ss.BulkModelProviderID.Eq(m.ID)).Delete(); err != nil {
		t.Fatal(err)
	}
	if err := s.Delete(ctx, m.ID); err != nil {
		t.Fatal(err)
	}
	_, err := s.Get(ctx, m.ID)
	wantErr(t, err, 404, "模型不存在")
}

func TestValidation(t *testing.T) {
	s := setup(t)
	for name, mut := range map[string]func(*SaveInput){
		"unknown provider": func(in *SaveInput) { in.Provider = "x" },
		"bad effort":       func(in *SaveInput) { in.ReasoningEffort = ptr("hi there") },
		"bad window":       func(in *SaveInput) { in.ContextWindow = ptr(0) },
		"no model":         func(in *SaveInput) { in.Model = "" },
	} {
		in := input(name)
		mut(&in)
		var e *errs.Error
		if _, err := s.Create(context.Background(), in); !errors.As(err, &e) || e.Status != 400 {
			t.Fatalf("%s: %v", name, err)
		}
	}
}
