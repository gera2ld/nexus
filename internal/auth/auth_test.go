package auth

import (
	"net/http/httptest"
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
		"/dav/files/a.txt":             {ScopeFiles},
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

func TestNormalizePaths(t *testing.T) {
	for _, c := range []struct {
		in   []string
		want []string
	}{
		{nil, nil},
		{[]string{"calendars/personal"}, []string{"/calendars/personal/"}},
		{[]string{"/files/docs/", " /files/docs/ ", "/contacts/"}, []string{"/contacts/", "/files/docs/"}},
		{[]string{"/"}, []string{"/"}},
		// Addresses as the dashboard shows them: /dav and the user's own id go.
		{[]string{"/dav/files/u1/docs/", "/dav/u1/calendars/personal/"}, []string{"/calendars/personal/", "/files/docs/"}},
		{[]string{"/dav/files/"}, []string{"/files/"}},
		{[]string{"/dav/"}, []string{"/"}},
		{[]string{"/dav/u1/"}, []string{"/"}},
	} {
		got, err := NormalizePaths(c.in, "u1")
		if err != nil {
			t.Errorf("NormalizePaths(%q): %v", c.in, err)
			continue
		}
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("NormalizePaths(%q) = %v, want %v", c.in, got, c.want)
		}
	}
	for _, in := range [][]string{
		{""}, {"/files/x/../y/"}, {"/api/"}, {"/dav/api/"}, {`/files\x/`},
		// Calendar and contact entries stop at a collection.
		{"/calendars/personal/e1.ics"}, {"/dav/u2/calendars/personal/"}, {"/x/"},
	} {
		if got, err := NormalizePaths(in, "u1"); err == nil {
			t.Errorf("NormalizePaths(%q) = %v, want error", in, got)
		}
	}
	// Every invalid pattern is named, not just the first.
	_, err := NormalizePaths([]string{"/x/", "/files/ok/", "/api/"}, "u1")
	if err == nil || !strings.Contains(err.Error(), `"/x/"`) || !strings.Contains(err.Error(), `"/api/"`) || strings.Contains(err.Error(), "/files/ok/") {
		t.Errorf("errors for two bad patterns: %v", err)
	}
}

func TestPathAllowed(t *testing.T) {
	if !PathAllowed("/dav/u1/calendars/personal/x.ics", "u1", nil) {
		t.Error("empty restriction should allow everything")
	}
	for _, c := range []struct {
		path    string
		allowed []string
		want    bool
	}{
		{"/dav/u1/", []string{"/calendars/personal/"}, true},           // discovery
		{"/dav/u1/calendars/", []string{"/calendars/personal/"}, true}, // listing
		{"/dav/u1/calendars/personal/", []string{"/calendars/personal/"}, true},
		{"/dav/u1/calendars/personal/x.ics", []string{"/calendars/personal/"}, true},
		{"/dav/u1/calendars/personal2/", []string{"/calendars/personal/"}, false},
		{"/dav/u1/contacts/contacts/", []string{"/calendars/personal/"}, false},
		{"/dav/u2/calendars/personal/", []string{"/calendars/personal/"}, false},
		{"/dav/u1/calendars/personal/../private/", []string{"/calendars/personal/"}, false},
		{"/dav/files/u1/docs/a.txt", []string{"/calendars/personal/"}, false},
		{"/dav/files/u1/", []string{"/files/docs/"}, true},
		{"/dav/files/u1/docs/a.txt", []string{"/files/docs/"}, true},
		{"/dav/files/u1/documents/a.txt", []string{"/files/docs/"}, false},
		{"/dav/u1/", []string{"/files/docs/"}, true}, // discovery: the principal lists nothing private
		{"/dav/u1/calendars/personal/", []string{"/"}, true},
	} {
		got := PathAllowed(c.path, "u1", c.allowed)
		if got != c.want {
			t.Errorf("PathAllowed(%q, %v) = %v, want %v", c.path, c.allowed, got, c.want)
		}
	}
}

func TestRequestAllowedParentsOnlyDiscover(t *testing.T) {
	allowed := []string{"/files/docs/a/b/"}
	for _, c := range []struct {
		method, path, depth string
		want                bool
	}{
		{"PROPFIND", "/dav/files/u1/docs/a/", "1", true},
		{"PROPFIND", "/dav/files/u1/docs/a/", "0", true},
		{"PROPFIND", "/dav/files/u1/docs/a/", "infinity", false},
		{"PROPFIND", "/dav/files/u1/docs/a/", "", false},
		{"OPTIONS", "/dav/files/u1/docs/a/", "", true},
		{"DELETE", "/dav/files/u1/docs/a/", "", false},
		{"COPY", "/dav/files/u1/docs/a/", "", false},
		{"MOVE", "/dav/files/u1/docs/a/", "", false},
		{"LOCK", "/dav/files/u1/docs/a/", "", false},
		{"PUT", "/dav/files/u1/docs/a", "", false},
		{"MKCOL", "/dav/files/u1/docs/a/", "", false},
		{"PROPPATCH", "/dav/files/u1/docs/a/", "", false},
		{"GET", "/dav/files/u1/docs/a/", "", false},
		{"DELETE", "/dav/files/u1/docs/a/b/x.txt", "", true},
		{"PROPFIND", "/dav/files/u1/docs/a/b/", "infinity", true},
	} {
		r := httptest.NewRequest(c.method, c.path, nil)
		if c.depth != "" {
			r.Header.Set("Depth", c.depth)
		}
		if got := RequestAllowed(r, "u1", allowed); got != c.want {
			t.Errorf("%s %s (Depth %q) = %v, want %v", c.method, c.path, c.depth, got, c.want)
		}
	}
}
