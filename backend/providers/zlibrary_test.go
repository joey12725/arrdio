package providers

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"github.com/joey12725/arrdio/backend/jobs"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

type transportFunc func(*http.Request) (*http.Response, error)

func (f transportFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func TestZLibraryTransferRestartDoesNotSpendAgain(t *testing.T) {
	root := t.TempDir()
	store, e := jobs.Open(filepath.Join(root, "jobs.db"))
	if e != nil {
		t.Fatal(e)
	}
	defer store.Close()
	key := bytes.Repeat([]byte{42}, 32)
	runtime, e := jobs.NewRuntime(store, key)
	if e != nil {
		t.Fatal(e)
	}
	n := 8
	runtime.ValidateAccount = func(context.Context, string, string) (*int, *int64, error) { return &n, nil, nil }
	if e = runtime.AddAccount(context.Background(), "fixture@example.invalid", "fixture-password"); e != nil {
		t.Fatal(e)
	}
	accounts, _ := runtime.Accounts(context.Background())
	accountID := accounts[0].ID
	var payload bytes.Buffer
	z := zip.NewWriter(&payload)
	m, _ := z.Create("mimetype")
	m.Write([]byte("application/epub+zip"))
	container, _ := z.Create("META-INF/container.xml")
	container.Write([]byte(`<container><rootfiles><rootfile full-path="content.opf"/></rootfiles></container>`))
	pkg, _ := z.Create("content.opf")
	pkg.Write([]byte(`<package><metadata><title>A</title></metadata></package>`))
	page, _ := z.Create("book.xhtml")
	page.Write([]byte("<html><body>Book</body></html>"))
	z.Close()
	var fileCalls, downloadCalls atomic.Int32
	checkpoint := filepath.Join(root, "checkpoint.json")
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/eapi/user/profile":
			w.Write([]byte(`{"user":{"daily_remaining":8}}`))
		case "/eapi/book/1/abcdef":
			w.Write([]byte(`{"book":{"title":"A","extension":"epub"}}`))
		case "/eapi/book/1/abcdef/file":
			fileCalls.Add(1)
			w.Write([]byte(`{"file":{"downloadLink":"https://fixture.z-library.ec/book.epub?token=fixture-signed-token"}}`))
		case "/book.epub":
			if _, e := os.Stat(checkpoint); e != nil {
				w.WriteHeader(500)
				return
			}
			if downloadCalls.Add(1) == 1 {
				w.WriteHeader(503)
				return
			}
			w.Write(payload.Bytes())
		default:
			w.WriteHeader(404)
		}
	}))
	defer server.Close()
	target, _ := url.Parse(server.URL)
	client := server.Client()
	transport := client.Transport
	client.Transport = transportFunc(func(req *http.Request) (*http.Response, error) {
		copy := req.Clone(req.Context())
		u := *req.URL
		u.Host = target.Host
		copy.URL = &u
		return transport.RoundTrip(copy)
	})
	newSource := func() *ZLibrary {
		return &ZLibrary{Runtime: runtime, sessions: map[string]*zSession{"fixture@example.invalid": {client: client, host: "fixture.z-library.ec", trusted: []string{"z-library.ec"}, created: time.Now(), credentialDigest: sha256.Sum256([]byte("fixture-password"))}}}
	}
	extra, _ := json.Marshal(map[string]int64{"account_id": accountID})
	candidate := jobs.Candidate{ID: "zlib:1:abcdef", Provider: "zlibrary", Title: "A", Format: "ebook", Extension: "epub", Extra: extra}
	ctx := jobs.WithTransferCheckpoint(context.Background(), func(receipt jobs.Receipt) error {
		data, e := json.Marshal(receipt)
		if e != nil {
			return e
		}
		if bytes.Contains(data, []byte("fixture-signed-token")) {
			return errors.New("plaintext transfer secret")
		}
		return os.WriteFile(checkpoint, data, 0600)
	})
	receipt, e := newSource().Acquire(ctx, candidate, root)
	if e == nil || fileCalls.Load() != 1 || downloadCalls.Load() != 1 {
		t.Fatal(receipt, e, fileCalls.Load(), downloadCalls.Load())
	}
	data, e := os.ReadFile(checkpoint)
	if e != nil {
		t.Fatal(e)
	}
	json.Unmarshal(data, &receipt)
	recovered, e := newSource().Reconcile(context.Background(), receipt)
	if e != nil || recovered.State != "verified" || fileCalls.Load() != 1 || downloadCalls.Load() != 2 {
		t.Fatal(recovered, e, fileCalls.Load(), downloadCalls.Load())
	}
	if e = Validate(recovered.Paths[0]); e != nil {
		t.Fatal(e)
	}
}
func TestCachedLoginCannotValidateDifferentSubmittedPassword(t *testing.T) {
	previous := http.DefaultTransport
	defer func() { http.DefaultTransport = previous }()
	loginCalls, profileCalls := 0, 0
	transport := transportFunc(func(req *http.Request) (*http.Response, error) {
		w := httptest.NewRecorder()
		w.Header().Set("Content-Type", "application/json")
		switch req.URL.Path {
		case "/eapi/info/domains":
			w.Write([]byte(`{"domains":["z-library.ec"]}`))
		case "/eapi/user/login":
			loginCalls++
			if e := req.ParseForm(); e != nil {
				t.Fatal(e)
			}
			if req.Form.Get("password") != "incorrect-new-password" {
				t.Fatal("new credentials were not submitted for verification")
			}
			w.Write([]byte(`{"success":0}`))
		case "/eapi/user/profile":
			profileCalls++
			w.Write([]byte(`{"success":1,"user":{"daily_remaining":8}}`))
		default:
			t.Fatalf("unexpected provider operation: %s", req.URL.Path)
		}
		return w.Result(), nil
	})
	http.DefaultTransport = transport
	source := &ZLibrary{sessions: map[string]*zSession{"fixture@example.invalid": {client: &http.Client{Transport: transport}, host: "z-library.ec", trusted: []string{"z-library.ec"}, created: time.Now(), credentialDigest: sha256.Sum256([]byte("valid-existing-password"))}}}
	if _, _, e := source.Profile(context.Background(), "fixture@example.invalid", "valid-existing-password"); e != nil {
		t.Fatal(e)
	}
	if _, _, e := source.Profile(context.Background(), "fixture@example.invalid", "incorrect-new-password"); e == nil {
		t.Fatal("incorrect password accepted using another credential's cached session")
	}
	if loginCalls != 1 || profileCalls != 1 {
		t.Fatal("incorrect credential verification path", loginCalls, profileCalls)
	}
	source.sessions["fixture@example.invalid"].credentialDigest = sha256.Sum256([]byte("incorrect-new-password"))
	if _, _, e := source.VerifyCredentials(context.Background(), "fixture@example.invalid", "incorrect-new-password"); e == nil {
		t.Fatal("fresh verification reused an existing login for a password the provider now rejects")
	}
	if loginCalls != 2 {
		t.Fatal("intake did not perform a new login", loginCalls)
	}
}
