package auth

import "testing"

func TestTokenMatches(t *testing.T) {
	s, err := NewService(Options{Token: "secret-token", BindHost: "127.0.0.1"})
	if err != nil {
		t.Fatal(err)
	}
	for name, tc := range map[string]struct {
		candidate string
		want      bool
	}{
		"exact":       {"secret-token", true},
		"empty":       {"", false},
		"prefix":      {"secret", false},
		"longer":      {"secret-token-x", false},
		"other token": {"other-token!", false},
	} {
		if got := s.TokenMatches(tc.candidate); got != tc.want {
			t.Errorf("%s: TokenMatches(%q) = %v", name, tc.candidate, got)
		}
	}
	if err := s.Login(" secret-token ", "1.2.3.4"); err != nil {
		t.Errorf("login trims whitespace: %v", err)
	}
	if err := s.Bootstrap(" secret-token"); err == nil {
		t.Error("bootstrap must not trim")
	}
}

func TestNewServiceRequiresToken(t *testing.T) {
	if _, err := NewService(Options{}); err == nil {
		t.Fatal("empty token must fail: auth is never off")
	}
}

func TestHostAllowed(t *testing.T) {
	loop, _ := NewService(Options{Token: "t", BindHost: "127.0.0.1"})
	public, _ := NewService(Options{Token: "t", BindHost: "0.0.0.0"})
	for host, want := range map[string]bool{
		"127.0.0.1:8080":     true,
		"localhost:8080":     true,
		"[::1]:8080":         true,
		"app.localhost":      true,
		"evil.example":       false,
		"evil.example:8080":  false,
		"127.0.0.1.evil.com": false,
	} {
		if got := loop.HostAllowed(host); got != want {
			t.Errorf("loopback bind: HostAllowed(%q) = %v, want %v", host, got, want)
		}
		if !public.HostAllowed(host) {
			t.Errorf("public bind serves any Host, rejected %q", host)
		}
	}
}

func TestNewTokenIsRandom(t *testing.T) {
	a, _ := NewToken()
	b, _ := NewToken()
	if len(a) < 40 || a == b {
		t.Fatalf("tokens %q %q", a, b)
	}
}
