package auth

import (
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestSecretFormat(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 100; i++ {
		s := NewSecret()
		g := strings.Split(s, "-")
		if len(g) != 6 || len(g[0]) != 4 || len(g[3]) != 4 || len(g[4]) != 5 || len(g[5]) != 5 {
			t.Fatalf("secret %q: want groups 4-4-4-4-5-5", s)
		}
		if strings.ToLower(s) != s || strings.ContainsAny(s, "018") {
			t.Fatalf("secret %q: want lowercase base32", s)
		}
		if seen[s] {
			t.Fatalf("duplicate secret %q", s)
		}
		seen[s] = true
	}
}

func TestHashSecretNormalises(t *testing.T) {
	s := NewSecret()
	want := HashSecret(s)
	for _, v := range []string{strings.ToUpper(s), strings.ReplaceAll(s, "-", ""), strings.ReplaceAll(s, "-", " ")} {
		if HashSecret(v) != want {
			t.Errorf("HashSecret(%q) differs", v)
		}
	}
	if HashSecret(NewSecret()) == want {
		t.Error("different secrets hash equal")
	}
}

func TestRequiredScopes(t *testing.T) {
	for p, want := range map[string][]string{
		"/dav/":                        {ScopeCal, ScopeCard},
		"/dav/u1/":                     {ScopeCal, ScopeCard},
		"/dav/u1/calendars/":           {ScopeCal},
		"/dav/u1/calendars/personal/x": {ScopeCal},
		"/dav/u1/contacts/contacts/":   {ScopeCard},
		"/dav/u1/other/":               {ScopeCal, ScopeCard},
		"/files/a.txt":                 {ScopeFiles},
		"/api/x":                       nil,
	} {
		if got := RequiredScopes(p); !reflect.DeepEqual(got, want) {
			t.Errorf("RequiredScopes(%q) = %v, want %v", p, got, want)
		}
	}
}

func TestLimiterWindow(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	l := NewLimiter(3, time.Minute)
	l.now = func() time.Time { return now }

	for i := 0; i < 3; i++ {
		if l.Blocked("a") {
			t.Fatalf("blocked after %d failures", i)
		}
		l.Fail("a")
	}
	if !l.Blocked("a") {
		t.Fatal("not blocked after 3 failures")
	}
	if l.Blocked("b") {
		t.Error("other address blocked")
	}
	now = now.Add(time.Minute)
	if l.Blocked("a") {
		t.Error("still blocked after the window")
	}
}

func TestLimiterBoundsMemory(t *testing.T) {
	l := NewLimiter(3, time.Minute)
	for i := 0; i < maxTracked+10; i++ {
		l.Fail(string(rune(i)) + "x")
	}
	if len(l.seen) > maxTracked {
		t.Errorf("tracking %d addresses, cap is %d", len(l.seen), maxTracked)
	}
}
