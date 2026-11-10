package jobs

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func runtimeTest(t *testing.T, path string) *Runtime {
	t.Helper()
	s := openTest(t, path)
	r, e := NewRuntime(s, bytes.Repeat([]byte{42}, 32))
	if e != nil {
		t.Fatal(e)
	}
	r.GatewayKey = strings.Repeat("gateway-test", 4)
	r.PublicURL = "https://arrdio.example/agent"
	r.Staging = t.TempDir()
	return r
}
func call(t *testing.T, r *Runtime, method, path, token string, body any) *httptest.ResponseRecorder {
	t.Helper()
	data, _ := json.Marshal(body)
	req := httptest.NewRequest(method, path, bytes.NewReader(data))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("X-Arrdio-Resource", r.PublicURL)
	req.Header.Set("X-Arrdio-User", "owner-1")
	w := httptest.NewRecorder()
	r.Handler().ServeHTTP(w, req)
	return w
}
func TestPersistentPairingAndScopes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "jobs.db")
	r := runtimeTest(t, path)
	bootstrap := randomToken()
	r.Store.db.Exec(`INSERT INTO bootstraps VALUES(?,?,?,0)`, tokenHash(bootstrap), "owner-1", time.Now().Add(time.Minute).Unix())
	payload := map[string]string{"bootstrap": bootstrap, "resource": r.PublicURL, "label": "test client"}
	w := call(t, r, "POST", "/pair", "", payload)
	if w.Code != 201 {
		t.Fatal(w.Code, w.Body.String())
	}
	var result map[string]string
	json.Unmarshal(w.Body.Bytes(), &result)
	token := result["token"]
	if w = call(t, r, "POST", "/pair", "", payload); w.Code != 401 {
		t.Fatal("bootstrap replay", w.Code)
	}
	r.Store.Close()
	r = runtimeTest(t, path)
	if w = call(t, r, "GET", "/me", token, nil); w.Code != 200 || !strings.Contains(w.Body.String(), "owner-1") {
		t.Fatal(w.Code, w.Body.String())
	}
	if w = call(t, r, "POST", "/accounts", token, map[string]string{}); w.Code != 403 {
		t.Fatal("agent account access", w.Code)
	}
	r.Store.db.Exec(`UPDATE grants SET scopes='status' WHERE hash=?`, tokenHash(token))
	if w = call(t, r, "POST", "/requests", token, map[string]any{}); w.Code != 403 {
		t.Fatal("wrong scope", w.Code)
	}
	if w = call(t, r, "GET", "/status", token, nil); w.Code != 200 {
		t.Fatal(w.Code)
	}
	r.Store.db.Exec(`UPDATE grants SET revoked=1 WHERE hash=?`, tokenHash(token))
	if w = call(t, r, "GET", "/me", token, nil); w.Code != 401 {
		t.Fatal("revoked token", w.Code)
	}
}
func TestEncryptedPoolQuotaAndReservations(t *testing.T) {
	r := runtimeTest(t, filepath.Join(t.TempDir(), "jobs.db"))
	left := 3
	reset := time.Now().Add(time.Hour).Unix()
	r.ValidateAccount = func(context.Context, string, string) (*int, *int64, error) { return &left, &reset, nil }
	for _, email := range []string{"one@example.invalid", "two@example.invalid"} {
		if e := r.AddAccount(ctx, email, "test-secret"); e != nil {
			t.Fatal(e)
		}
	}
	q, e := r.Quota(ctx)
	if e != nil || q["remaining"] != 6 || q["reset"] != nil {
		t.Fatal(q, e)
	}
	r.Store.db.Exec(`UPDATE accounts SET remaining=2`)
	q, e = r.Quota(ctx)
	if e != nil || q["remaining"] != 4 || q["reset"] == nil {
		t.Fatal(q, e)
	}
	var secret []byte
	r.Store.db.QueryRow(`SELECT secret FROM accounts LIMIT 1`).Scan(&secret)
	if bytes.Contains(secret, []byte("test-secret")) {
		t.Fatal("plaintext secret")
	}
	batch := enqueue(t, r.Store, "jobs", Input{Title: "A"}, Input{Title: "B"}, Input{Title: "C"})
	var ids []int64
	for _, rid := range batch.RequestIDs {
		var id int64
		r.Store.db.QueryRow(`SELECT id FROM representations WHERE request_id=? AND format='ebook'`, rid).Scan(&id)
		ids = append(ids, id)
	}
	r.Store.db.Exec(`UPDATE representations SET state='matching',lease_token='test-owner',lease_until=?`, time.Now().Add(time.Minute).Unix())
	a, e := r.Reserve(ctx, ids[0])
	if e != nil {
		t.Fatal(e)
	}
	b, e := r.Reserve(ctx, ids[1])
	if e != nil || a == b {
		t.Fatal("account serialization", a, b, e)
	}
	if _, e = r.Reserve(ctx, ids[2]); e == nil {
		t.Fatal("concurrent account reservation")
	}
	q, e = r.Quota(ctx)
	if e != nil || q["remaining"] != 2 {
		t.Fatal(q, e)
	}
	r.Store.db.Exec(`UPDATE accounts SET healthy=0 WHERE id=?`, a)
	q, e = r.Quota(ctx)
	if e != nil || q["remaining"] != 1 {
		t.Fatal("unusable account affected usable household balance", q, e)
	}
}

type fakeLibrary struct {
	offline   bool
	available bool
	imports   int
}

func (l *fakeLibrary) Presence(context.Context, Input, string) ([]Presence, error) {
	state := "absent"
	if l.offline {
		state = "unknown"
	}
	if l.available {
		state = "available"
	}
	return []Presence{{Service: "fixture", State: state}}, nil
}
func (l *fakeLibrary) Import(context.Context, Input, string, []string, string) ([]Presence, error) {
	l.imports++
	return []Presence{{Service: "fixture", State: "available"}}, nil
}

type fakeSource struct {
	searches, acquires int
	empty              bool
}

func (s *fakeSource) Name() string { return "fixture" }
func (s *fakeSource) Search(context.Context, Input, string) ([]Candidate, error) {
	s.searches++
	if s.empty {
		return nil, nil
	}
	return []Candidate{{ID: "book", Provider: "fixture", Title: "A", Format: "ebook"}}, nil
}
func (s *fakeSource) Acquire(context.Context, Candidate, string) (Receipt, error) {
	s.acquires++
	return Receipt{ID: "receipt", State: "verified", Paths: []string{"fixture.epub"}}, nil
}
func (s *fakeSource) Reconcile(_ context.Context, r Receipt) (Receipt, error) { return r, nil }
func TestWorkerPresenceFallbackAndPartial(t *testing.T) {
	r := runtimeTest(t, filepath.Join(t.TempDir(), "jobs.db"))
	l := &fakeLibrary{offline: true}
	first := &fakeSource{empty: true}
	second := &fakeSource{}
	r.Library = l
	r.Sources = []Source{first, second}
	b := enqueue(t, r.Store, "a", Input{Title: "A"})
	r.step(ctx, "ebook")
	if second.acquires != 0 {
		t.Fatal("download while presence unknown")
	}
	r.Store.db.Exec(`UPDATE representations SET state='queued',eligible_at=0 WHERE format='ebook'`)
	l.offline = false
	r.step(ctx, "ebook")
	if first.searches != 1 || second.acquires != 1 || l.imports != 1 {
		t.Fatal(first, second, l)
	}
	rows, e := r.Store.List(ctx, 1, 0, "queue")
	if e != nil || rows[0].ID != b.RequestIDs[0] {
		t.Fatal(e)
	}
	states := map[string]string{}
	for _, c := range rows[0].Representations {
		states[c.Format] = c.State
	}
	if states["ebook"] != "available" || states["audio"] != "queued" {
		t.Fatal(states)
	}
	r.step(ctx, "ebook")
	if second.acquires != 1 {
		t.Fatal("duplicate acquisition")
	}
}
