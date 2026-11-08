package logging

import (
	"bytes"
	"errors"
	"log"
	"log/slog"
	"strings"
	"testing"
)

func TestRedactingHandler(t *testing.T) {
	var buf bytes.Buffer
	l := slog.New(NewRedactingHandler(slog.NewTextHandler(&buf, nil)))
	l.Info("call with Authorization: Bearer abc.def-123 done",
		"api_key", "sk-live-1", "user", "bob",
		"err", errors.New("dial: password=hunter2 refused"),
		"url", "postgres://u:pgpass@host/db")
	out := buf.String()
	for _, leak := range []string{"abc.def-123", "sk-live-1", "hunter2", "pgpass"} {
		if strings.Contains(out, leak) {
			t.Fatalf("leaked %q: %s", leak, out)
		}
	}
	if !strings.Contains(out, "bob") {
		t.Fatalf("over-redacted: %s", out)
	}
}

func TestSetupBridgesLogPackage(t *testing.T) {
	var buf bytes.Buffer
	prev := slog.Default()
	t.Cleanup(func() { slog.SetDefault(prev) })
	Setup(&buf)
	log.Printf("connecting with token=s3cr3t")
	if strings.Contains(buf.String(), "s3cr3t") {
		t.Fatalf("log package output not redacted: %s", buf.String())
	}
}
