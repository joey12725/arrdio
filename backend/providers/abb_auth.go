package providers

import (
	"context"
	"errors"
	"golang.org/x/net/html"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"sync"
	"time"
)

func (a *ABB) newClient() (*http.Client, error) {
	base, e := url.Parse(a.URL)
	if e != nil || base.Host == "" || base.User != nil || (base.Scheme != "https" && base.Scheme != "http") {
		return nil, errors.New("ABB origin invalid")
	}
	jar, _ := cookiejar.New(nil)
	return &http.Client{Timeout: 30 * time.Second, Jar: jar, Transport: &pacedTransport{next: http.DefaultTransport, interval: abbPace, cooldown: abbCooldown}, CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) > 5 || req.URL.Host != base.Host || req.URL.User != nil {
			return errors.New("ABB cross-origin redirect rejected")
		}
		if base.Scheme == "https" && req.URL.Scheme == "http" {
			req.URL.Scheme = "https"
		}
		if req.URL.Scheme != base.Scheme {
			return errors.New("ABB insecure redirect rejected")
		}
		return nil
	}}, nil
}

// abbPace spaces every request to ABB, including logins and redirects. Bursts
// of detail pages got the client's IP blocked during live testing, and so did
// about 80 requests in ten minutes at a 2 s spacing.
var abbPace = 4 * time.Second

// abbCooldown silences ABB after it refuses or drops a request. A blocked IP
// stays blocked while it keeps knocking; retrying every minute (searches) and
// every five (probes) would only extend the block.
var abbCooldown = 3 * time.Hour

var errCoolingDown = errors.New("ABB is cooling down after a refused request")

type pacedTransport struct {
	next     http.RoundTripper
	interval time.Duration
	cooldown time.Duration
	mu       sync.Mutex
	last     time.Time
	until    time.Time
}

func (p *pacedTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	p.mu.Lock()
	if time.Now().Before(p.until) {
		p.mu.Unlock()
		return nil, errCoolingDown
	}
	slot := time.Now()
	if next := p.last.Add(p.interval); next.After(slot) {
		slot = next
	}
	p.last = slot
	p.mu.Unlock()
	if wait := time.Until(slot); wait > 0 {
		timer := time.NewTimer(wait)
		defer timer.Stop()
		select {
		case <-r.Context().Done():
			return nil, r.Context().Err()
		case <-timer.C:
		}
	}
	response, e := p.next.RoundTrip(r)
	refused := e != nil && r.Context().Err() == nil
	if e == nil {
		switch response.StatusCode {
		case http.StatusForbidden, http.StatusTooManyRequests, http.StatusServiceUnavailable:
			refused = true
		}
	}
	if refused && p.cooldown > 0 {
		p.mu.Lock()
		p.until = time.Now().Add(p.cooldown)
		p.mu.Unlock()
	}
	return response, e
}

func abbDetailURL(base *url.URL, href string) (*url.URL, error) {
	u, e := url.Parse(href)
	if e != nil {
		return nil, errors.New("invalid ABB detail URL")
	}
	u = base.ResolveReference(u)
	if u.Host != base.Host || u.User != nil {
		return nil, errors.New("untrusted ABB detail URL")
	}
	if base.Scheme == "https" && u.Scheme == "http" {
		u.Scheme = "https"
	}
	if u.Scheme != base.Scheme || !strings.HasPrefix(u.Path, "/abss/") {
		return nil, errors.New("untrusted ABB detail URL")
	}
	return u, nil
}
func (a *ABB) authenticatedClient(ctx context.Context) (*http.Client, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.client == nil {
		client, e := a.newClient()
		if e != nil {
			return nil, e
		}
		a.client = client
	}
	if a.Username == "" && a.Password == "" && a.Cookie == "" {
		return a.client, nil
	}
	if time.Since(a.verifiedAt) < 10*time.Minute {
		return a.client, nil
	}
	// One member-page check keeps a live session; a full login only when it lapsed.
	if a.verifiedAt.IsZero() || a.verifyMember(ctx) != nil {
		if e := a.login(ctx); e != nil {
			return nil, e
		}
	}
	a.verifiedAt = time.Now()
	return a.client, nil
}
func (a *ABB) VerifyLogin(ctx context.Context) error {
	if a.Username == "" || a.Password == "" {
		if a.Cookie == "" {
			return errors.New("ABB credentials not configured")
		}
	}
	_, e := a.authenticatedClient(ctx)
	return e
}
func (a *ABB) login(ctx context.Context) error {
	endpoint := strings.TrimRight(a.URL, "/") + "/member/login.php"
	if a.Username != "" || a.Password != "" {
		if a.Username == "" || a.Password == "" {
			return errors.New("ABB credentials incomplete")
		}
		request, e := http.NewRequestWithContext(ctx, "GET", endpoint, nil)
		if e != nil {
			return e
		}
		request.Header.Set("User-Agent", "Mozilla/5.0 (compatible; ARRDIO/2.0)")
		response, e := a.client.Do(request)
		if e != nil {
			return errors.New("ABB login unavailable")
		}
		io.Copy(io.Discard, io.LimitReader(response.Body, 2<<20))
		response.Body.Close()
		if response.StatusCode != 200 {
			return errors.New("ABB login unavailable")
		}
		form := url.Values{"username": {a.Username}, "password": {a.Password}}
		request, e = http.NewRequestWithContext(ctx, "POST", endpoint, strings.NewReader(form.Encode()))
		if e != nil {
			return e
		}
		request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		request.Header.Set("User-Agent", "Mozilla/5.0 (compatible; ARRDIO/2.0)")
		request.Header.Set("Referer", endpoint)
		response, e = a.client.Do(request)
		if e != nil {
			return errors.New("ABB authentication failed")
		}
		io.Copy(io.Discard, io.LimitReader(response.Body, 2<<20))
		response.Body.Close()
		if response.StatusCode != 200 {
			return errors.New("ABB authentication rejected")
		}
	}
	return a.verifyMember(ctx)
}

// verifyMember confirms the current session by its member page logout link.
func (a *ABB) verifyMember(ctx context.Context) error {
	request, e := http.NewRequestWithContext(ctx, "GET", strings.TrimRight(a.URL, "/")+"/member/users/", nil)
	if e != nil {
		return e
	}
	request.Header.Set("User-Agent", "Mozilla/5.0 (compatible; ARRDIO/2.0)")
	if a.Cookie != "" {
		request.Header.Set("Cookie", a.Cookie)
	}
	response, e := a.client.Do(request)
	if e != nil {
		return errors.New("ABB member verification unavailable")
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		return errors.New("ABB credentials could not be verified")
	}
	doc, e := html.Parse(io.LimitReader(response.Body, 2<<20))
	if e != nil {
		return errors.New("ABB member response invalid")
	}
	base, _ := url.Parse(a.URL)
	verified := false
	var walk func(*html.Node)
	walk = func(node *html.Node) {
		if node.Type == html.ElementNode && node.Data == "a" {
			for _, attr := range node.Attr {
				if attr.Key != "href" {
					continue
				}
				u, e := url.Parse(attr.Val)
				if e != nil {
					continue
				}
				u = base.ResolveReference(u)
				if u.Host == base.Host && (u.Path == "/member/logout" || u.Path == "/member/logout.php" || u.Path == "/member/logout/") {
					verified = true
				}
			}
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(doc)
	if !verified {
		return errors.New("ABB username/password rejected")
	}
	return nil
}
