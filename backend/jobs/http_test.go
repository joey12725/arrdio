package jobs

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

func TestHTTPAuthorizationAndInput(t *testing.T) {
	s := openTest(t, filepath.Join(t.TempDir(), "jobs.db"))
	token := strings.Repeat("test-only-", 4)
	h := s.Handler(token)
	for _, test := range []struct {
		method, path, body, auth, key string
		status                        int
	}{
		{"GET", "/requests", "", "", "", 401},
		{"POST", "/requests", `{"items":[{"title":"A"}]}`, "wrong", "one", 401},
		{"POST", "/requests", `{"items":[{"title":"A"}]}`, token, "one", 202},
		{"POST", "/requests", `{"items":[{"title":"A"}]}`, token, "one", 202},
		{"POST", "/requests", `{"items":[{"title":"B"}]}`, token, "one", 409},
		{"POST", "/requests", `{"items":[{"title":"B","destination":"/etc"}]}`, token, "two", 400},
		{"POST", "/requests", `{"items":[{"title":"B"}]} {}`, token, "two", 400},
		{"GET", "/requests?sort=title", "", token, "", 200},
		{"GET", "/requests?limit=10000", "", token, "", 400},
		{"GET", "/requests?sort=invalid", "", token, "", 400},
		{"POST", "/requests/1/cancel?format=ebook", "", token, "", 204},
		{"POST", "/requests/100/cancel", "", token, "", 404},
		{"GET", "/status", "", token, "", 200},
	} {
		r := httptest.NewRequest(test.method, test.path, strings.NewReader(test.body))
		if test.auth != "" {
			r.Header.Set("Authorization", "Bearer "+test.auth)
		}
		r.Header.Set("Idempotency-Key", test.key)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != test.status {
			t.Fatalf("%s %s: %d %s", test.method, test.path, w.Code, w.Body.String())
		}
	}
	w := httptest.NewRecorder()
	s.Handler("").ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/status", nil))
	if w.Code != 503 {
		t.Fatal(w.Code)
	}
	counts, e := s.Counts(ctx)
	if e != nil || counts["canceled"] != 1 || counts["queued"] != 1 {
		t.Fatal(counts, e)
	}
}
