// Z-Library EAPI contracts ported from an earlier arrdio-acquisition/zlibrary.py prototype.
// No dependency on that prototype's runtime, fixed ten-credit assumptions, or disabled TLS.
package providers

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/ashkaty/arrdio-api/jobs"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

func escape(s string) string { return url.QueryEscape(s) }
func coverURL(id int) string { return fmt.Sprintf("https://covers.openlibrary.org/b/id/%d-M.jpg", id) }

var zID = regexp.MustCompile(`^zlib:(\d+):([a-f0-9]{6})$`)

type zSession struct {
	client           *http.Client
	host             string
	trusted          []string
	mu               sync.Mutex
	created          time.Time
	credentialDigest [32]byte
}
type ZLibrary struct {
	Runtime  *jobs.Runtime
	mu       sync.Mutex
	sessions map[string]*zSession
}

func (z *ZLibrary) Name() string { return "zlibrary" }

// VerifyCredentials performs a new non-consuming login and profile request.
// Even a matching cached password/session is not proof the password is still valid.
func (z *ZLibrary) VerifyCredentials(ctx context.Context, email, password string) (*int, *int64, error) {
	fresh := &ZLibrary{}
	return fresh.Profile(ctx, email, password)
}
func trustedURL(raw string, hosts []string) bool {
	u, e := url.Parse(raw)
	if e != nil || u.Scheme != "https" || u.User != nil || (u.Port() != "" && u.Port() != "443") {
		return false
	}
	if net.ParseIP(u.Hostname()) != nil || !strings.Contains(u.Hostname(), ".") {
		return false
	}
	for _, h := range hosts {
		if strings.EqualFold(u.Hostname(), h) || strings.HasSuffix(strings.ToLower(u.Hostname()), "."+strings.ToLower(h)) {
			return true
		}
	}
	return false
}
func (z *ZLibrary) session(ctx context.Context, email, password string) (*zSession, error) {
	z.mu.Lock()
	defer z.mu.Unlock()
	if z.sessions == nil {
		z.sessions = map[string]*zSession{}
	}
	credentialDigest := sha256.Sum256([]byte(password))
	if s := z.sessions[email]; s != nil && s.credentialDigest == credentialDigest && time.Since(s.created) < 45*time.Minute {
		return s, nil
	}
	roots := []string{"z-library.ec", "z-library.sk", "1lib.sk", "ncdn.ec"}
	s := &zSession{trusted: roots, created: time.Now(), credentialDigest: credentialDigest}
	jar, _ := cookiejar.New(nil)
	s.client = &http.Client{Timeout: 45 * time.Second, Jar: jar, CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) > 5 || !trustedURL(req.URL.String(), s.trusted) {
			return errors.New("untrusted source redirect")
		}
		return nil
	}}
	for _, host := range roots {
		var p struct {
			Domains []json.RawMessage `json:"domains"`
		}
		if e := jsonCall(ctx, s.client, "GET", "https://"+host+"/eapi/info/domains", nil, nil, &p); e != nil || len(p.Domains) == 0 {
			continue
		}
		s.host = host
		for _, raw := range p.Domains {
			var domain string
			json.Unmarshal(raw, &domain)
			if domain == "" {
				var d struct {
					Domain string `json:"domain"`
				}
				json.Unmarshal(raw, &d)
				domain = d.Domain
			}
			if regexp.MustCompile(`^[a-zA-Z0-9.-]+$`).MatchString(domain) {
				s.trusted = append(s.trusted, domain)
			}
		}
		break
	}
	if s.host == "" {
		return nil, errors.New("no trusted EAPI domain available")
	}
	var login struct {
		Success int `json:"success"`
		User    struct {
			ID  int    `json:"id"`
			Key string `json:"remix_userkey"`
		} `json:"user"`
	}
	e := s.call(ctx, "/eapi/user/login", url.Values{"email": {email}, "password": {password}}, &login)
	if e != nil || login.Success != 1 || login.User.ID == 0 || login.User.Key == "" {
		return nil, errors.New("source authentication failed")
	}
	u, _ := url.Parse("https://" + s.host)
	s.client.Jar.SetCookies(u, []*http.Cookie{{Name: "remix_userid", Value: strconv.Itoa(login.User.ID), Secure: true, Path: "/"}, {Name: "remix_userkey", Value: login.User.Key, Secure: true, Path: "/"}})
	z.sessions[email] = s
	return s, nil
}
func (s *zSession) call(ctx context.Context, path string, form url.Values, out any) error {
	method := "GET"
	var body io.Reader
	headers := map[string]string{"User-Agent": "ARRDIO/2.0", "Accept": "application/json"}
	if form != nil {
		method = "POST"
		body = strings.NewReader(form.Encode())
		headers["Content-Type"] = "application/x-www-form-urlencoded"
	}
	return jsonCall(ctx, s.client, method, "https://"+s.host+path, body, headers, out)
}
func count(v any) *int {
	var n int
	switch v := v.(type) {
	case float64:
		if v < 0 || v != float64(int(v)) {
			return nil
		}
		n = int(v)
	case string:
		x, e := strconv.Atoi(v)
		if e != nil || x < 0 {
			return nil
		}
		n = x
	default:
		return nil
	}
	return &n
}
func (z *ZLibrary) Profile(ctx context.Context, email, password string) (*int, *int64, error) {
	s, e := z.session(ctx, email, password)
	if e != nil {
		return nil, nil, e
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	var p map[string]any
	if e = s.call(ctx, "/eapi/user/profile", nil, &p); e != nil {
		var status HTTPStatusError
		if errors.As(e, &status) && (status.Code == 401 || status.Code == 403) {
			z.forget(email)
		}
		return nil, nil, e
	}
	if flag, exists := p["success"]; exists {
		success := count(flag)
		if success == nil || *success != 1 {
			z.forget(email)
			return nil, nil, errors.New("profile rejected authentication or contract")
		}
	}
	u, ok := p["user"].(map[string]any)
	if !ok {
		z.forget(email)
		return nil, nil, errors.New("malformed profile")
	}
	n := profileRemaining(u)
	if n == nil {
		return nil, nil, errors.New("unknown source quota")
	}
	var reset *int64
	if v, ok := u["daily_reset"].(string); ok {
		if t, e := time.Parse(time.RFC3339, v); e == nil {
			x := t.Unix()
			reset = &x
		}
	}
	return n, reset, nil
}
func (z *ZLibrary) forget(email string) { z.mu.Lock(); delete(z.sessions, email); z.mu.Unlock() }
func (z *ZLibrary) Search(ctx context.Context, in jobs.Input, format string) ([]jobs.Candidate, error) {
	if format != "ebook" {
		return nil, nil
	}
	accounts, e := z.Runtime.Accounts(ctx)
	if e != nil {
		return nil, e
	}
	for _, a := range accounts {
		if !a.Healthy {
			continue
		}
		email, pw, e := z.Runtime.AccountCredentials(ctx, a.ID)
		if e != nil {
			continue
		}
		s, e := z.session(ctx, email, pw)
		if e != nil {
			continue
		}
		var p struct {
			Success int `json:"success"`
			Books   []struct {
				ID        json.Number `json:"id"`
				Hash      string      `json:"hash"`
				Title     string      `json:"title"`
				Author    string      `json:"author"`
				Language  string      `json:"language"`
				Edition   string      `json:"edition"`
				Extension string      `json:"extension"`
				Cover     string      `json:"cover"`
			} `json:"books"`
		}
		language := in.Constraints.Language
		if language == "" {
			language = "english"
		}
		queries := []struct{ title, language string }{{in.Title, language}}
		if base, _, ok := strings.Cut(in.Title, ":"); ok {
			queries = append(queries, struct{ title, language string }{strings.TrimSpace(base), language})
		}
		if in.Constraints.Language == "" {
			queries = append(queries, struct{ title, language string }{in.Title, ""})
		}
		for _, query := range queries {
			form := url.Values{"message": {strings.TrimSpace(query.title + " " + in.Author)}, "limit": {"50"}, "page": {"1"}, "extensions[]": {"epub", "pdf"}}
			if query.language != "" {
				form.Set("languages[]", query.language)
			}
			p.Books = nil
			p.Success = 0
			s.mu.Lock()
			e = s.call(ctx, "/eapi/book/search", form, &p)
			s.mu.Unlock()
			if e != nil {
				continue
			}
			if p.Success != 1 {
				z.forget(email)
				return nil, errors.New("source search rejected authentication or contract")
			}
			out := []jobs.Candidate{}
			for _, b := range p.Books {
				if b.Extension != "epub" && b.Extension != "pdf" {
					continue
				}
				id := "zlib:" + b.ID.String() + ":" + b.Hash
				if !zID.MatchString(id) {
					continue
				}
				out = append(out, jobs.Candidate{ID: id, Provider: z.Name(), Title: b.Title, Author: b.Author, Language: b.Language, Edition: b.Edition, Extension: b.Extension, Format: "ebook", Cover: b.Cover})
			}
			ranked := Rank(in, format, out)
			if len(ranked) > 0 {
				return ranked, nil
			}
		}
		return []jobs.Candidate{}, nil
	}
	return nil, errors.New("no healthy ebook account")
}
func (z *ZLibrary) Acquire(ctx context.Context, c jobs.Candidate, destination string) (jobs.Receipt, error) {
	receipt := jobs.Receipt{ID: c.ID, Title: c.Title, Author: c.Author, State: "unresolved"}
	m := zID.FindStringSubmatch(c.ID)
	if m == nil {
		return receipt, jobs.SafeFailure{Err: jobs.ErrInvalid}
	}
	var account struct {
		ID int64 `json:"account_id"`
	}
	if json.Unmarshal(c.Extra, &account) != nil {
		return receipt, jobs.SafeFailure{Err: jobs.ErrInvalid}
	}
	email, pw, e := z.Runtime.AccountCredentials(ctx, account.ID)
	if e != nil {
		return receipt, jobs.SafeFailure{Err: e}
	}
	s, e := z.session(ctx, email, pw)
	if e != nil {
		return receipt, jobs.SafeFailure{Err: e}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	path := filepath.Join(destination, "book."+c.Extension)
	receipt.Paths = []string{path}
	receipt.AccountID = account.ID
	if _, e = os.Stat(path); e == nil {
		if Validate(path) == nil {
			if e = VerifyBookIdentity(path, c); e != nil {
				return receipt, jobs.SafeFailure{Err: e}
			}
			receipt.State = "verified"
			return receipt, nil
		}
		return receipt, jobs.SafeFailure{Err: errors.New("existing artifact invalid")}
	}
	var detail struct {
		Book struct {
			Extension string `json:"extension"`
			Title     string `json:"title"`
		} `json:"book"`
	}
	if e = s.call(ctx, "/eapi/book/"+m[1]+"/"+m[2], nil, &detail); e != nil {
		return receipt, jobs.SafeFailure{Err: e}
	}
	if detail.Book.Extension != c.Extension || detail.Book.Title == "" {
		return receipt, jobs.SafeFailure{Err: errors.New("candidate metadata changed")}
	}
	// Refresh provider truth under the same account lock immediately before /file.
	var profile map[string]any
	if e = s.call(ctx, "/eapi/user/profile", nil, &profile); e != nil {
		return receipt, jobs.SafeFailure{Err: e}
	}
	if flag, exists := profile["success"]; exists {
		success := count(flag)
		if success == nil || *success != 1 {
			z.forget(email)
			return receipt, jobs.SafeFailure{Err: errors.New("profile rejected authentication or contract")}
		}
	}
	u, ok := profile["user"].(map[string]any)
	if !ok {
		return receipt, jobs.SafeFailure{Err: errors.New("unknown quota")}
	}
	remaining := profileRemaining(u)
	if remaining == nil {
		return receipt, jobs.SafeFailure{Err: errors.New("unknown quota")}
	}
	if *remaining == 0 {
		return receipt, jobs.SafeFailure{Err: jobs.ErrQuotaExhausted}
	}
	var file struct {
		File struct {
			Link string `json:"downloadLink"`
		} `json:"file"`
	}
	if !jobs.HasTransferCheckpoint(ctx) {
		return receipt, jobs.SafeFailure{Err: errors.New("durable transfer checkpoint required")}
	}
	if e = s.call(ctx, "/eapi/book/"+m[1]+"/"+m[2]+"/file", nil, &file); e != nil {
		return receipt, e
	}
	base, _ := url.Parse("https://" + s.host)
	link, e := url.Parse(file.File.Link)
	if e != nil || file.File.Link == "" {
		return receipt, errors.New("source link unavailable")
	}
	target := base.ResolveReference(link).String()
	if !trustedURL(target, s.trusted) {
		return receipt, errors.New("untrusted source link")
	}
	receipt.TransferLink, e = z.Runtime.SealTransferURL(target)
	if e != nil {
		return receipt, e
	}
	if e = jobs.CheckpointTransfer(ctx, receipt); e != nil {
		return receipt, e
	}
	return z.download(ctx, s, receipt)
}
func (z *ZLibrary) download(ctx context.Context, s *zSession, receipt jobs.Receipt) (jobs.Receipt, error) {
	target, e := z.Runtime.OpenTransferURL(receipt.TransferLink)
	if e != nil || !trustedURL(target, s.trusted) || len(receipt.Paths) != 1 {
		return receipt, errors.New("transfer checkpoint unavailable")
	}
	path := receipt.Paths[0]
	req, _ := http.NewRequestWithContext(ctx, "GET", target, nil)
	client := *s.client
	client.Timeout = 3 * time.Minute
	resp, e := client.Do(req)
	if e != nil {
		return receipt, errors.New("transfer interrupted")
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		if resp.StatusCode == 404 || resp.StatusCode == 410 {
			return receipt, jobs.FinalTransferFailure{Err: errors.New("source transfer expired")}
		}
		return receipt, errors.New("transfer rejected")
	}
	data, e := io.ReadAll(io.LimitReader(resp.Body, (256<<20)+1))
	if e != nil || len(data) > 256<<20 {
		return receipt, errors.New("transfer limit exceeded")
	}
	tmp := path + ".verify" + filepath.Ext(path)
	if e = atomicWrite(tmp, data); e != nil {
		return receipt, e
	}
	if e = Validate(tmp); e != nil {
		os.Remove(tmp)
		return receipt, jobs.FinalTransferFailure{Err: e}
	}
	if e = VerifyBookIdentity(tmp, jobs.Candidate{Title: receipt.Title, Author: receipt.Author}); e != nil {
		os.Remove(tmp)
		return receipt, jobs.FinalTransferFailure{Err: e}
	}
	if e = os.Rename(tmp, path); e != nil {
		return receipt, e
	}
	receipt.State = "verified"
	return receipt, nil
}
func (z *ZLibrary) Prepare(ctx context.Context, c jobs.Candidate, destination string) (jobs.Receipt, error) {
	if !zID.MatchString(c.ID) || (c.Extension != "epub" && c.Extension != "pdf") {
		return jobs.Receipt{}, jobs.ErrInvalid
	}
	var account struct {
		ID int64 `json:"account_id"`
	}
	json.Unmarshal(c.Extra, &account)
	return jobs.Receipt{ID: c.ID, Title: c.Title, Author: c.Author, State: "unresolved", AccountID: account.ID, Paths: []string{filepath.Join(destination, "book."+c.Extension)}}, nil
}
func profileRemaining(u map[string]any) *int {
	n := count(u["daily_remaining"])
	if n == nil {
		n = count(u["downloads_left"])
	}
	if n == nil {
		for _, pair := range [][2]string{{"downloads_today", "downloads_limit"}, {"daily_amount", "daily_allowed"}} {
			used, limit := count(u[pair[0]]), count(u[pair[1]])
			if used != nil && limit != nil {
				x := *limit - *used
				if x < 0 {
					x = 0
				}
				return &x
			}
		}
	}
	return n
}
func (z *ZLibrary) Reconcile(ctx context.Context, r jobs.Receipt) (jobs.Receipt, error) {
	if len(r.Paths) == 1 && Validate(r.Paths[0]) == nil {
		if e := VerifyBookIdentity(r.Paths[0], jobs.Candidate{Title: r.Title, Author: r.Author}); e != nil {
			return r, jobs.FinalTransferFailure{Err: e}
		}
		r.State = "verified"
		return r, nil
	}
	if len(r.TransferLink) == 0 || r.AccountID == 0 {
		return r, jobs.ErrUnresolvedTransfer
	}
	email, password, e := z.Runtime.AccountCredentials(ctx, r.AccountID)
	if e != nil {
		return r, e
	}
	s, e := z.session(ctx, email, password)
	if e != nil {
		return r, e
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return z.download(ctx, s, r)
}
