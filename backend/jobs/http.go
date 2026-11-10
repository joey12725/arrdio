package jobs

import (
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
)

// Handler is an operator-only staging API until browser and scoped agent grants
// are integrated. It fails closed when no strong staging token is configured.
func (s *Store) Handler(token string) http.Handler {
	mux := http.NewServeMux()
	write := func(w http.ResponseWriter, status int, v any) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		json.NewEncoder(w).Encode(v)
	}
	fail := func(w http.ResponseWriter, e error) {
		status := 500
		message := "internal error"
		switch {
		case errors.Is(e, ErrInvalid):
			status = 400
			message = "invalid request"
		case errors.Is(e, ErrConflict):
			status = 409
			message = ErrConflict.Error()
		case errors.Is(e, sql.ErrNoRows):
			status = 404
			message = "not found"
		}
		write(w, status, map[string]string{"error": message})
	}
	mux.HandleFunc("POST /requests", func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, 16<<20)
		defer r.Body.Close()
		var in struct {
			Items []Input `json:"items"`
		}
		d := json.NewDecoder(r.Body)
		d.DisallowUnknownFields()
		if e := d.Decode(&in); e != nil {
			fail(w, ErrInvalid)
			return
		}
		if e := d.Decode(new(any)); e != io.EOF {
			fail(w, ErrInvalid)
			return
		}
		principal, _ := r.Context().Value(principalKey{}).(string)
		if principal == "" {
			principal = "staging-operator"
		}
		b, e := s.Enqueue(r.Context(), principal, r.Header.Get("Idempotency-Key"), in.Items)
		if e != nil {
			fail(w, e)
			return
		}
		write(w, 202, b)
	})
	mux.HandleFunc("GET /requests", func(w http.ResponseWriter, r *http.Request) {
		limit, offset := 50, 0
		var e error
		if q := r.URL.Query().Get("limit"); q != "" {
			limit, e = strconv.Atoi(q)
			if e != nil {
				fail(w, ErrInvalid)
				return
			}
		}
		if q := r.URL.Query().Get("offset"); q != "" {
			offset, e = strconv.Atoi(q)
			if e != nil {
				fail(w, ErrInvalid)
				return
			}
		}
		sort := r.URL.Query().Get("sort")
		if sort == "" {
			sort = "queue"
		}
		rows, e := s.List(r.Context(), limit, offset, sort, r.URL.Query().Get("q"))
		if e != nil {
			fail(w, e)
			return
		}
		write(w, 200, rows)
	})
	mux.HandleFunc("GET /status", func(w http.ResponseWriter, r *http.Request) {
		counts, e := s.Counts(r.Context())
		if e != nil {
			fail(w, e)
			return
		}
		write(w, 200, map[string]any{"representations": counts, "dispatch_enabled": false})
	})
	mux.HandleFunc("POST /requests/{id}/{action}", func(w http.ResponseWriter, r *http.Request) {
		id, e := strconv.ParseInt(r.PathValue("id"), 10, 64)
		if e != nil {
			fail(w, ErrInvalid)
			return
		}
		if e = s.Control(r.Context(), id, r.URL.Query().Get("format"), r.PathValue("action")); e != nil {
			fail(w, e)
			return
		}
		w.WriteHeader(204)
	})
	expected := sha256.Sum256([]byte(token))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		if len(token) < 32 {
			write(w, 503, map[string]string{"error": "staging API not configured"})
			return
		}
		auth := r.Header.Get("Authorization")
		got := sha256.Sum256([]byte(strings.TrimPrefix(auth, "Bearer ")))
		if !strings.HasPrefix(auth, "Bearer ") || subtle.ConstantTimeCompare(got[:], expected[:]) != 1 {
			w.Header().Set("WWW-Authenticate", "Bearer")
			write(w, 401, map[string]string{"error": "unauthorized"})
			return
		}
		mux.ServeHTTP(w, r)
	})
}
