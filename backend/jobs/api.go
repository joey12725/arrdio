package jobs

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

type principalKey struct{}
type scopesKey struct{}

func scopeAllowed(ctx context.Context, scope string) bool {
	scopes, _ := ctx.Value(scopesKey{}).(string)
	return strings.Contains(" "+scopes+" ", " "+scope+" ")
}

func respond(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}
func decode(w http.ResponseWriter, r *http.Request, v any) error {
	r.Body = http.MaxBytesReader(w, r.Body, 16<<20)
	defer r.Body.Close()
	d := json.NewDecoder(r.Body)
	d.DisallowUnknownFields()
	if e := d.Decode(v); e != nil {
		return ErrInvalid
	}
	if e := d.Decode(new(any)); e != io.EOF {
		return ErrInvalid
	}
	return nil
}
func (r *Runtime) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.Handle("/mcp", r.MCP())
	mux.Handle("/", r.Store.Handler(r.GatewayKey))
	mux.HandleFunc("GET /status", func(w http.ResponseWriter, q *http.Request) {
		counts, e := r.Store.Counts(q.Context())
		if e != nil {
			respond(w, 500, map[string]string{"error": "status unavailable"})
			return
		}
		respond(w, 200, map[string]any{"representations": counts, "dispatch_enabled": r.Enabled, "services": r.Services()})
	})
	mux.HandleFunc("GET /me", func(w http.ResponseWriter, q *http.Request) {
		respond(w, 200, map[string]any{"owner": q.Context().Value(principalKey{}), "service": "arrdio", "resource": r.PublicURL})
	})
	mux.HandleFunc("GET /quota", func(w http.ResponseWriter, q *http.Request) {
		v, e := r.Quota(q.Context())
		if e != nil {
			respond(w, 503, map[string]string{"error": "quota unavailable"})
			return
		}
		respond(w, 200, v)
	})
	mux.HandleFunc("GET /accounts", func(w http.ResponseWriter, q *http.Request) {
		a, e := r.Accounts(q.Context())
		if e != nil {
			respond(w, 500, map[string]string{"error": "accounts unavailable"})
			return
		} // account metadata only; no passwords/cookies
		summaries := []map[string]any{}
		for _, x := range a {
			summaries = append(summaries, map[string]any{"id": x.ID, "email": x.Email, "healthy": x.Healthy})
		}
		respond(w, 200, summaries)
	})
	mux.HandleFunc("POST /accounts", func(w http.ResponseWriter, q *http.Request) {
		var in struct {
			Email    string `json:"email"`
			Password string `json:"password"`
		}
		if decode(w, q, &in) != nil {
			respond(w, 400, map[string]string{"error": "invalid account"})
			return
		}
		if e := r.AddAccount(q.Context(), in.Email, in.Password); e != nil {
			respond(w, 422, map[string]string{"error": "Account could not be validated; no allowance was consumed"})
			return
		}
		respond(w, 201, map[string]bool{"added": true})
	})
	mux.HandleFunc("GET /catalog", func(w http.ResponseWriter, q *http.Request) {
		query := strings.TrimSpace(q.URL.Query().Get("q"))
		if len(query) < 2 {
			respond(w, 200, []Candidate{})
			return
		}
		if len(query) > 500 || r.SearchCatalog == nil {
			respond(w, 503, map[string]string{"error": "catalog unavailable"})
			return
		}
		v, e := r.SearchCatalog(q.Context(), query)
		if e != nil {
			respond(w, 503, map[string]string{"error": "catalog unavailable"})
			return
		}
		respond(w, 200, v)
	})
	mux.HandleFunc("POST /bootstrap", func(w http.ResponseWriter, q *http.Request) {
		owner, _ := q.Context().Value(principalKey{}).(string)
		token := randomToken()
		_, e := r.Store.db.ExecContext(q.Context(), `INSERT INTO bootstraps(hash,owner,expires) VALUES(?,?,?)`, tokenHash(token), owner, time.Now().Add(15*time.Minute).Unix())
		if e != nil {
			respond(w, 500, map[string]string{"error": "pairing unavailable"})
			return
		}
		prompt := "Connect to my private ARRDIO acquisition service at " + r.PublicURL + ". Download all three files: " + r.PublicURL + "/integration/SKILL.md, " + r.PublicURL + "/integration/client.py and " + r.PublicURL + "/integration/API.md. Follow the skill to pair via POST /pair with bootstrap " + token + " and resource " + r.PublicURL + ". The bootstrap expires in 15 minutes and can be used once. Persist the returned grant using your secret mechanism. Verify GET /me and GET /status. Configure the downloaded client as a stdio MCP server if supported; otherwise use its CLI. Never log tokens or include them in source. Normal grants persist until explicitly revoked."
		respond(w, 201, map[string]string{"prompt": prompt})
	})
	mux.HandleFunc("GET /grants", func(w http.ResponseWriter, q *http.Request) {
		owner := q.Context().Value(principalKey{})
		rows, e := r.Store.db.QueryContext(q.Context(), `SELECT substr(hash,1,16),label,created,revoked FROM grants WHERE owner=?`, owner)
		if e != nil {
			respond(w, 500, map[string]string{"error": "grants unavailable"})
			return
		}
		defer rows.Close()
		out := []map[string]any{}
		for rows.Next() {
			var id, label string
			var created int64
			var revoked bool
			rows.Scan(&id, &label, &created, &revoked)
			out = append(out, map[string]any{"id": id, "label": label, "created": created, "revoked": revoked})
		}
		respond(w, 200, out)
	})
	mux.HandleFunc("POST /grants/{id}/revoke", func(w http.ResponseWriter, q *http.Request) {
		id := q.PathValue("id")
		if len(id) != 16 {
			respond(w, 400, map[string]string{"error": "invalid grant"})
			return
		}
		r.Store.db.ExecContext(q.Context(), `UPDATE grants SET revoked=1 WHERE owner=? AND substr(hash,1,16)=?`, q.Context().Value(principalKey{}), id)
		w.WriteHeader(204)
	})
	mux.HandleFunc("GET /routing", func(w http.ResponseWriter, q *http.Request) {
		rows, e := r.Store.db.QueryContext(q.Context(), `SELECT category,format,library,library_id FROM routing ORDER BY category`)
		if e != nil {
			respond(w, 500, map[string]string{"error": "routing unavailable"})
			return
		}
		defer rows.Close()
		out := []map[string]string{}
		for rows.Next() {
			var category, format, library, id string
			rows.Scan(&category, &format, &library, &id)
			out = append(out, map[string]string{"category": category, "format": format, "library": library, "library_id": id})
		}
		respond(w, 200, out)
	})
	mux.HandleFunc("POST /categorize", func(w http.ResponseWriter, q *http.Request) {
		var in struct {
			IDs      []int64 `json:"ids"`
			Category string  `json:"category"`
		}
		if decode(w, q, &in) != nil || len(in.IDs) == 0 || len(in.IDs) > 1000 {
			respond(w, 400, map[string]string{"error": "invalid category assignment"})
			return
		}
		if e := r.Categorize(q.Context(), in.IDs, in.Category); e != nil {
			respond(w, 409, map[string]string{"error": "Category changes require compatible formats, retained placement sources, and no active import"})
			return
		}
		w.WriteHeader(204)
	})
	mux.HandleFunc("POST /photos", r.uploadPhoto)
	mux.HandleFunc("POST /refresh", func(w http.ResponseWriter, q *http.Request) {
		var in struct {
			ID     int64  `json:"id"`
			Format string `json:"format"`
		}
		if decode(w, q, &in) != nil {
			respond(w, 400, map[string]string{"error": "invalid refresh"})
			return
		}
		if e := r.Refresh(q.Context(), in.ID, in.Format); e != nil {
			respond(w, 503, map[string]string{"error": "Library refresh unavailable; existing jobs retained"})
			return
		}
		respond(w, 202, map[string]string{"state": "refresh_requested"})
	})
	mux.HandleFunc("GET /candidates", func(w http.ResponseWriter, q *http.Request) {
		id, e := strconv.ParseInt(q.URL.Query().Get("id"), 10, 64)
		if e != nil {
			respond(w, 400, map[string]string{"error": "invalid request"})
			return
		}
		items, e := r.Candidates(q.Context(), id, q.URL.Query().Get("format"))
		if e != nil {
			respond(w, 503, map[string]string{"error": "candidate search unavailable"})
			return
		}
		respond(w, 200, items)
	})
	mux.HandleFunc("POST /correct", func(w http.ResponseWriter, q *http.Request) {
		var in struct {
			ID        int64  `json:"id"`
			Format    string `json:"format"`
			Candidate string `json:"candidate"`
		}
		if decode(w, q, &in) != nil {
			respond(w, 400, map[string]string{"error": "invalid correction"})
			return
		}
		if e := r.Correct(q.Context(), in.ID, in.Format, in.Candidate); e != nil {
			respond(w, 409, map[string]string{"error": "Cannot replace an active or unresolved transfer; reconcile it first"})
			return
		}
		w.WriteHeader(204)
	})
	mux.HandleFunc("GET /photos", func(w http.ResponseWriter, q *http.Request) {
		owner, _ := q.Context().Value(principalKey{}).(string)
		out, e := r.Photos(q.Context(), owner)
		if e != nil {
			respond(w, 500, map[string]string{"error": "photos unavailable"})
			return
		}
		respond(w, 200, out)
	})
	return http.HandlerFunc(func(w http.ResponseWriter, q *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		if q.URL.Path == "/pair" && q.Method == "POST" {
			r.pair(w, q)
			return
		}
		if strings.HasPrefix(q.URL.Path, "/integration/") {
			r.integration(w, q)
			return
		}
		if q.URL.Path == "/.well-known/oauth-protected-resource" {
			respond(w, 200, map[string]any{"resource": r.PublicURL, "bearer_methods_supported": []string{"header"}, "scopes_supported": []string{"acquire", "status", "categorize"}, "resource_documentation": r.PublicURL + "/integration/API.md"})
			return
		}
		bearer := strings.TrimPrefix(q.Header.Get("Authorization"), "Bearer ")
		unauthorized := func(message string) {
			w.Header().Set("WWW-Authenticate", `Bearer realm="arrdio", resource_metadata="`+r.PublicURL+`/.well-known/oauth-protected-resource"`)
			respond(w, 401, map[string]string{"error": message})
		}
		if bearer == q.Header.Get("Authorization") || bearer == "" {
			unauthorized("authentication required")
			return
		}
		owner := ""
		scopes := "acquire status categorize"
		gateway := len(r.GatewayKey) >= 32 && subtle.ConstantTimeCompare([]byte(bearer), []byte(r.GatewayKey)) == 1
		if gateway {
			owner = q.Header.Get("X-Arrdio-User")
			if owner == "" {
				owner = "operator"
			}
		} else {
			resource := q.Header.Get("X-Arrdio-Resource")
			if resource != r.PublicURL {
				unauthorized("wrong resource")
				return
			}
			if e := r.Store.db.QueryRowContext(q.Context(), `SELECT owner,scopes FROM grants WHERE hash=? AND revoked=0`, tokenHash(bearer)).Scan(&owner, &scopes); e != nil {
				unauthorized("invalid grant")
				return
			}
			var audience string
			r.Store.db.QueryRowContext(q.Context(), `SELECT resource FROM grant_resources WHERE hash=?`, tokenHash(bearer)).Scan(&audience)
			if audience != "" && audience != r.PublicURL {
				unauthorized("grant belongs to a different resource")
				return
			}
			if audience == "" {
				if _, e := r.Store.db.ExecContext(q.Context(), `INSERT OR IGNORE INTO grant_resources VALUES(?,?)`, tokenHash(bearer), r.PublicURL); e != nil {
					respond(w, 503, map[string]string{"error": "grant state unavailable"})
					return
				}
			}
			if strings.HasPrefix(q.URL.Path, "/accounts") || q.URL.Path == "/bootstrap" || strings.HasPrefix(q.URL.Path, "/grants") {
				respond(w, 403, map[string]string{"error": "browser session required"})
				return
			}
			required := "status"
			if q.Method != "GET" && q.Method != "HEAD" {
				required = "acquire"
			}
			if q.URL.Path == "/categorize" || q.URL.Path == "/refresh" {
				required = "categorize"
			}
			if q.URL.Path != "/mcp" && !strings.Contains(" "+scopes+" ", " "+required+" ") {
				respond(w, 403, map[string]string{"error": "insufficient scope"})
				return
			}
		}
		clone := q.Clone(context.WithValue(context.WithValue(q.Context(), principalKey{}, owner), scopesKey{}, scopes))
		clone.Header = q.Header.Clone()
		clone.Header.Set("Authorization", "Bearer "+r.GatewayKey)
		mux.ServeHTTP(w, clone)
	})
}
func (r *Runtime) pair(w http.ResponseWriter, q *http.Request) {
	var in struct {
		Bootstrap string `json:"bootstrap"`
		Resource  string `json:"resource"`
		Label     string `json:"label"`
	}
	if decode(w, q, &in) != nil || in.Resource != r.PublicURL || len(in.Label) > 100 {
		respond(w, 400, map[string]string{"error": "invalid pairing request"})
		return
	}
	tx, e := r.Store.db.BeginTx(q.Context(), nil)
	if e != nil {
		respond(w, 500, map[string]string{"error": "pairing unavailable"})
		return
	}
	defer tx.Rollback()
	var owner string
	e = tx.QueryRowContext(q.Context(), `UPDATE bootstraps SET used=1 WHERE hash=? AND used=0 AND expires>? RETURNING owner`, tokenHash(in.Bootstrap), time.Now().Unix()).Scan(&owner)
	if e != nil {
		respond(w, 401, map[string]string{"error": "bootstrap expired or already used"})
		return
	}
	token := randomToken()
	_, e = tx.ExecContext(q.Context(), `INSERT INTO grants(hash,owner,label,scopes,created) VALUES(?,?,?,?,?)`, tokenHash(token), owner, in.Label, "acquire status categorize", time.Now().Unix())
	if e == nil {
		_, e = tx.ExecContext(q.Context(), `INSERT INTO grant_resources VALUES(?,?)`, tokenHash(token), r.PublicURL)
	}
	if e != nil || tx.Commit() != nil {
		respond(w, 500, map[string]string{"error": "pairing unavailable"})
		return
	}
	respond(w, 201, map[string]string{"token": token, "resource": r.PublicURL, "owner": owner, "scopes": "acquire status categorize"})
}
func (r *Runtime) uploadPhoto(w http.ResponseWriter, q *http.Request) {
	var in PhotoInput
	if decode(w, q, &in) != nil {
		respond(w, 400, map[string]string{"error": "invalid image"})
		return
	}
	owner, _ := q.Context().Value(principalKey{}).(string)
	id, e := r.SavePhoto(q.Context(), owner, in)
	if e != nil {
		status := 500
		message := "upload unavailable"
		if errors.Is(e, ErrInvalid) {
			status = 400
			message = "Image type, size, dimensions or options are invalid"
		}
		if errors.Is(e, ErrPhotoCapacity) {
			status = 429
			message = "Let the current photo batch finish before adding more"
		}
		respond(w, status, map[string]string{"error": message})
		return
	}
	respond(w, 202, map[string]string{"id": id})
}
func (r *Runtime) processPhoto(ctx context.Context) {
	if r.ExtractPhoto == nil {
		return
	}
	r.Store.db.ExecContext(ctx, `UPDATE photo_units SET data=NULL WHERE state='failed' AND created<?`, time.Now().Add(-24*time.Hour).Unix())
	var id, owner, mime string
	var data, result []byte
	tx, e := r.Store.db.BeginTx(ctx, nil)
	if e != nil {
		return
	}
	defer tx.Rollback()
	e = tx.QueryRowContext(ctx, `SELECT p.hash,p.owner,p.data,p.mime,p.result FROM photo_units p LEFT JOIN photo_retries retry ON retry.hash=p.hash LEFT JOIN photo_leases lease ON lease.hash=p.hash WHERE p.state IN ('queued','extracting') AND COALESCE(retry.next_at,0)<=? AND COALESCE(lease.until,0)<=? ORDER BY p.created LIMIT 1`, time.Now().Unix(), time.Now().Unix()).Scan(&id, &owner, &data, &mime, &result)
	if e != nil {
		return
	}
	if _, e = tx.ExecContext(ctx, `INSERT INTO photo_leases VALUES(?,?) ON CONFLICT(hash) DO UPDATE SET until=excluded.until`, id, time.Now().Add(2*time.Minute).Unix()); e != nil {
		return
	}
	if _, e = tx.ExecContext(ctx, `UPDATE photo_units SET state='extracting' WHERE hash=?`, id); e != nil {
		return
	}
	if tx.Commit() != nil {
		return
	}
	defer r.Store.db.ExecContext(ctx, `DELETE FROM photo_leases WHERE hash=?`, id)
	var inputs []Input
	if len(result) > 0 {
		e = json.Unmarshal(result, &inputs)
	} else {
		inputs, e = r.ExtractPhoto(ctx, data, mime)
	}
	if e != nil {
		var attempts int
		r.Store.db.QueryRowContext(ctx, `INSERT INTO photo_retries(hash,attempts,next_at) VALUES(?,1,?) ON CONFLICT(hash) DO UPDATE SET attempts=attempts+1,next_at=excluded.next_at RETURNING attempts`, id, time.Now().Add(time.Minute).Unix()).Scan(&attempts)
		state := "queued"
		if attempts >= 3 {
			state = "failed"
		}
		r.Store.db.ExecContext(ctx, `UPDATE photo_units SET state=?,error='Extraction unavailable; re-upload to retry after automatic retries' WHERE hash=?`, state, id)
		return
	}
	if len(inputs) == 0 {
		r.Store.db.ExecContext(ctx, `UPDATE photo_units SET state='failed',error='No readable books found; try another photo or text',data=NULL WHERE hash=?`, id)
		return
	}
	var overrideKind, formatsJSON string
	r.Store.db.QueryRowContext(ctx, `SELECT kind,formats FROM photo_options WHERE hash=?`, id).Scan(&overrideKind, &formatsJSON)
	var overrideFormats []string
	json.Unmarshal([]byte(formatsJSON), &overrideFormats)
	for index := range inputs {
		if overrideKind != "" {
			inputs[index].Kind = overrideKind
		}
		if len(overrideFormats) > 0 {
			inputs[index].Formats = append([]string(nil), overrideFormats...)
		}
		var requirements []byte
		if r.Store.db.QueryRowContext(ctx, `SELECT data FROM photo_constraints WHERE hash=?`, id).Scan(&requirements) == nil {
			json.Unmarshal(requirements, &inputs[index].Constraints)
		}
	}
	result, _ = json.Marshal(inputs)
	if _, e = r.Store.db.ExecContext(ctx, `UPDATE photo_units SET result=? WHERE hash=?`, result, id); e != nil {
		return
	}
	batch, e := r.Store.Enqueue(ctx, owner, "photo:"+id, inputs)
	if e != nil {
		if errors.Is(e, ErrInvalid) || errors.Is(e, ErrConflict) {
			r.Store.db.ExecContext(ctx, `UPDATE photo_units SET state='failed',error='Extracted entries could not be accepted; re-upload or enter text' WHERE hash=?`, id)
		}
		return
	}
	message := ""
	if len(batch.Errors) > 0 {
		message = strconv.Itoa(len(batch.Errors)) + " extracted entries were invalid; valid books were queued. Add missing titles as text."
	}
	r.Store.db.ExecContext(ctx, `UPDATE photo_units SET state='complete',error=?,data=NULL WHERE hash=?`, message, id)
}
func (r *Runtime) integration(w http.ResponseWriter, q *http.Request) {
	if q.Method != "GET" {
		w.WriteHeader(405)
		return
	}
	name := strings.TrimPrefix(q.URL.Path, "/integration/")
	if name != "SKILL.md" && name != "API.md" && name != "client.py" {
		http.NotFound(w, q)
		return
	}
	data, e := integrationFiles.ReadFile("integration/" + name)
	if e != nil {
		http.NotFound(w, q)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Write(data)
}
func validPublicURL(v string) error {
	u, e := url.Parse(v)
	if e != nil || u.Host == "" || (u.Scheme != "https" && u.Scheme != "http") {
		return errors.New("public URL must be absolute")
	}
	return nil
}
