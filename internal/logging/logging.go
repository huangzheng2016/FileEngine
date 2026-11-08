// Package logging is the central log setup: slog with a redacting handler so
// tokens, API keys and passwords never reach log files, including output of
// the legacy log package (slog.SetDefault bridges it).
package logging

import (
	"context"
	"io"
	"log/slog"
	"regexp"
	"strings"
)

const redacted = "[REDACTED]"

var sensitiveKeys = []string{"token", "password", "passwd", "secret", "api_key", "apikey", "authorization", "dsn", "cookie"}

// Inline credentials in free-form messages and error strings.
var sensitivePatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)(bearer\s+)[A-Za-z0-9._~+/=-]+`),
	regexp.MustCompile(`(?i)((?:token|password|passwd|api_key|apikey|secret)\s*[=:]\s*)[^\s&"',;]+`),
	regexp.MustCompile(`(?i)(://[^/\s:@]+:)[^@\s/]+(@)`),
	regexp.MustCompile(`\b(sk-)[A-Za-z0-9_-]{8,}`),
}

// Redact masks credentials found in s.
func Redact(s string) string {
	for _, re := range sensitivePatterns {
		s = re.ReplaceAllStringFunc(s, func(m string) string {
			sub := re.FindStringSubmatch(m)
			out := sub[1] + redacted
			if len(sub) > 2 {
				out += sub[2]
			}
			return out
		})
	}
	return s
}

func isSensitiveKey(k string) bool {
	k = strings.ToLower(k)
	for _, s := range sensitiveKeys {
		if strings.Contains(k, s) {
			return true
		}
	}
	return false
}

type redactingHandler struct{ next slog.Handler }

// NewRedactingHandler wraps next, masking sensitive attribute values and
// credential patterns in messages and string values.
func NewRedactingHandler(next slog.Handler) slog.Handler { return redactingHandler{next} }

func (h redactingHandler) Enabled(ctx context.Context, l slog.Level) bool {
	return h.next.Enabled(ctx, l)
}

func (h redactingHandler) Handle(ctx context.Context, r slog.Record) error {
	out := slog.NewRecord(r.Time, r.Level, Redact(r.Message), r.PC)
	r.Attrs(func(a slog.Attr) bool {
		out.AddAttrs(redactAttr(a))
		return true
	})
	return h.next.Handle(ctx, out)
}

func (h redactingHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	red := make([]slog.Attr, len(attrs))
	for i, a := range attrs {
		red[i] = redactAttr(a)
	}
	return redactingHandler{h.next.WithAttrs(red)}
}

func (h redactingHandler) WithGroup(name string) slog.Handler {
	return redactingHandler{h.next.WithGroup(name)}
}

func redactAttr(a slog.Attr) slog.Attr {
	if isSensitiveKey(a.Key) {
		return slog.String(a.Key, redacted)
	}
	v := a.Value.Resolve()
	switch v.Kind() {
	case slog.KindGroup:
		g := v.Group()
		red := make([]any, len(g))
		for i, ga := range g {
			red[i] = redactAttr(ga)
		}
		return slog.Group(a.Key, red...)
	case slog.KindString:
		return slog.String(a.Key, Redact(v.String()))
	case slog.KindAny:
		if err, ok := v.Any().(error); ok {
			return slog.String(a.Key, Redact(err.Error()))
		}
	}
	return a
}

// Setup installs a redacting text logger writing to w as the process default
// (slog and the log package) and returns it.
func Setup(w io.Writer) *slog.Logger {
	l := slog.New(NewRedactingHandler(slog.NewTextHandler(w, nil)))
	slog.SetDefault(l)
	return l
}
