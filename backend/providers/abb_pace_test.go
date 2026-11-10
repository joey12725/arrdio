package providers

import (
	"context"
	"github.com/ashkaty/arrdio-api/jobs"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

// Fixtures run unpaced; TestABBRequestsArePaced covers pacing itself.
func init() { abbPace, abbCooldown, slskdPoll = 0, 0, time.Millisecond }

func TestABBRequestsArePaced(t *testing.T) {
	var times []time.Time
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		times = append(times, time.Now())
		w.Write([]byte("ok"))
	}))
	defer server.Close()
	client := &http.Client{Transport: &pacedTransport{next: http.DefaultTransport, interval: 80 * time.Millisecond}}
	for i := 0; i < 3; i++ {
		resp, e := client.Get(server.URL)
		if e != nil {
			t.Fatal(e)
		}
		resp.Body.Close()
	}
	for i := 1; i < len(times); i++ {
		if gap := times[i].Sub(times[i-1]); gap < 70*time.Millisecond {
			t.Fatal("requests not spaced", gap)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET", server.URL, nil)
	if _, e := client.Do(req); e == nil {
		t.Fatal("canceled request waited and was sent")
	}
}

func TestABBDetailFailureIsRetriedNotCached(t *testing.T) {
	var details atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/":
			w.Write([]byte(`<a href="/abss/book">Book</a>`))
		case "/abss/book":
			if details.Add(1) == 1 {
				w.WriteHeader(503)
				return
			}
			w.Write([]byte(`<title>Dracula - Bram Stoker Audiobook M4B</title><p>Written by Bram Stoker Format: M4B Bitrate: 64 Kbps</p>Info Hash: 0123456789abcdef0123456789abcdef01234567`))
		}
	}))
	defer server.Close()
	abb := &ABB{URL: server.URL}
	in := jobs.Input{Title: "Dracula", Author: "Bram Stoker"}
	if r, _ := abb.Search(context.Background(), in, "audio"); len(r) != 0 {
		t.Fatal("unavailable page produced a candidate", r)
	}
	if r, e := abb.Search(context.Background(), in, "audio"); e != nil || len(r) != 1 {
		t.Fatal("transient failure was cached", r, e, details.Load())
	}
}

func TestABBCoolsDownAfterRefusal(t *testing.T) {
	var hits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusForbidden)
	}))
	defer server.Close()
	client := &http.Client{Transport: &pacedTransport{next: http.DefaultTransport, cooldown: time.Hour}}
	resp, e := client.Get(server.URL)
	if e != nil {
		t.Fatal(e)
	}
	resp.Body.Close()
	if _, e = client.Get(server.URL); e == nil || hits.Load() != 1 {
		t.Fatalf("request sent during cool-down: hits=%d err=%v", hits.Load(), e)
	}
}
