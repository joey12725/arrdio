package providers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/ashkaty/arrdio-api/jobs"
	"golang.org/x/net/html"
	stdhtml "html"
	"io"
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

type Category struct {
	ID       int        `json:"id"`
	Name     string     `json:"name"`
	Children []Category `json:"subCategories"`
}
type Prowlarr struct {
	URL, Key string
	Torrent  *Torrent
}

func (p *Prowlarr) Name() string { return "prowlarr" }
func categoryIDs(categories []Category, kind string) []int {
	out := []int{}
	for _, c := range categories {
		n := strings.ToLower(c.Name)
		if (kind == "audio" && (strings.Contains(n, "audiobook") || strings.Contains(n, "audio book"))) || (kind == "manga" && (strings.Contains(n, "literature") || strings.Contains(n, "comics"))) {
			out = append(out, c.ID)
		}
		out = append(out, categoryIDs(c.Children, kind)...)
	}
	return out
}

// releaseTag reads the "Title by Author [LANG / FORMAT] [VIP]" convention that
// Prowlarr's MyAnonamouse definition puts in release titles. Untagged titles
// return empty facts, which constraints treat as unknown rather than matching.
var releaseTagPattern = regexp.MustCompile(`^(.*) by (.+?) \[([A-Za-z]{2,3}) / ([^\]]+)\](?:\s*\[[^\]]*\])*\s*$`)

func releaseTag(title string) (author, language, extension string) {
	m := releaseTagPattern.FindStringSubmatch(strings.TrimSpace(title))
	if m == nil || strings.TrimSpace(m[1]) == "" {
		return "", "", ""
	}
	if containers := strings.FieldsFunc(m[4], func(r rune) bool { return r == ' ' || r == ',' || r == '/' }); len(containers) == 1 {
		extension = strings.ToLower(containers[0])
	}
	return strings.TrimSpace(m[2]), strings.ToLower(m[3]), extension
}
func (p *Prowlarr) Search(ctx context.Context, in jobs.Input, format string) ([]jobs.Candidate, error) {
	if format == "ebook" || p.URL == "" {
		return nil, nil
	}
	var indexers []struct {
		ID           int    `json:"id"`
		Name         string `json:"name"`
		Enable       bool   `json:"enable"`
		Capabilities struct {
			Categories []Category `json:"categories"`
		} `json:"capabilities"`
	}
	headers := map[string]string{"X-Api-Key": p.Key}
	if e := jsonCall(ctx, Client, "GET", p.URL+"/api/v1/indexer", nil, headers, &indexers); e != nil {
		return nil, e
	}
	out := []jobs.Candidate{}
	for _, indexer := range indexers {
		if !indexer.Enable {
			continue
		}
		cats := categoryIDs(indexer.Capabilities.Categories, format)
		if len(cats) == 0 {
			continue
		}
		q := url.Values{"query": {strings.TrimSpace(in.Title + " " + in.Author + " " + in.Volume)}, "type": {"search"}, "indexerIds": {strconv.Itoa(indexer.ID)}}
		for _, id := range cats {
			q.Add("categories", strconv.Itoa(id))
		}
		var results []struct {
			GUID        string     `json:"guid"`
			Title       string     `json:"title"`
			DownloadURL string     `json:"downloadUrl"`
			MagnetURL   string     `json:"magnetUrl"`
			InfoHash    string     `json:"infoHash"`
			Seeders     *int       `json:"seeders"`
			Categories  []Category `json:"categories"`
		}
		if e := jsonCall(ctx, Client, "GET", p.URL+"/api/v1/search?"+q.Encode(), nil, headers, &results); e != nil {
			continue
		}
		for _, v := range results {
			if len(categoryIDs(v.Categories, format)) == 0 {
				continue
			}
			link := v.MagnetURL
			if link == "" {
				link = v.DownloadURL
			}
			extra, _ := json.Marshal(map[string]string{"hash": v.InfoHash, "format": format, "volume": in.Volume})
			author, language, extension := releaseTag(v.Title)
			out = append(out, jobs.Candidate{ID: v.GUID, Provider: p.Name(), Title: v.Title, Author: author, Language: language, Extension: extension, Format: format, Volume: volumeCoverage(v.Title), Seeders: v.Seeders, URL: link, Extra: extra})
		}
	}
	return Rank(in, format, out), nil
}
func (p *Prowlarr) Acquire(ctx context.Context, c jobs.Candidate, d string) (jobs.Receipt, error) {
	if p.Torrent == nil {
		return jobs.Receipt{}, errors.New("torrent client not configured")
	}
	return p.Torrent.Add(ctx, c, d)
}
func (p *Prowlarr) Prepare(ctx context.Context, c jobs.Candidate, d string) (jobs.Receipt, error) {
	if p.Torrent == nil {
		return jobs.Receipt{}, errors.New("torrent client not configured")
	}
	return p.Torrent.Prepare(ctx, c, d)
}
func (p *Prowlarr) Reconcile(ctx context.Context, r jobs.Receipt) (jobs.Receipt, error) {
	return p.Torrent.Poll(ctx, r)
}

type Torrent struct {
	URL, Username, Password, Category string
	DownloadRoot                      string
	Instance                          string
	client                            *http.Client
	mu                                sync.Mutex
}

func (t *Torrent) login(ctx context.Context) error {
	if t.client == nil {
		jar, _ := cookiejar.New(nil)
		t.client = &http.Client{Jar: jar, Timeout: 45 * time.Second, CheckRedirect: sameOriginRedirect}
	}
	form := url.Values{"username": {t.Username}, "password": {t.Password}}
	req, _ := http.NewRequestWithContext(ctx, "POST", t.URL+"/api/v2/auth/login", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, e := t.client.Do(req)
	if e != nil {
		return errors.New("torrent connection failed")
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 100))
	if resp.StatusCode != 200 || string(data) != "Ok." {
		return errors.New("torrent authentication failed")
	}
	return nil
}

var hashPattern = regexp.MustCompile(`^[a-fA-F0-9]{40}$`)

func (t *Torrent) Add(ctx context.Context, c jobs.Candidate, d string) (jobs.Receipt, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	r, e := t.Prepare(ctx, c, d)
	if e != nil {
		return r, jobs.SafeFailure{Err: e}
	}
	if e := t.login(ctx); e != nil {
		return r, jobs.SafeFailure{Err: e}
	}
	var extra map[string]string
	json.Unmarshal(c.Extra, &extra)
	hash := strings.ToLower(extra["hash"])
	if u, e := url.Parse(c.URL); e == nil && u.Scheme == "magnet" {
		hash = strings.TrimPrefix(u.Query().Get("xt"), "urn:btih:")
	}
	if !hashPattern.MatchString(hash) {
		return r, jobs.SafeFailure{Err: errors.New("torrent requires stable infohash before submission")}
	}
	r.ID = hash
	var existing []struct {
		Hash string `json:"hash"`
	}
	if e := jsonCall(ctx, t.client, "GET", t.URL+"/api/v2/torrents/info?hashes="+hash, nil, nil, &existing); e != nil {
		return r, jobs.SafeFailure{Err: e}
	}
	if len(existing) > 0 {
		return r, nil
	}
	// All paths/categories are operator-owned. Never accept save paths from callers.
	// autoTMM makes the category's save path authoritative even when the client's
	// default is manual mode; ARRDIO_TORRENT_ROOT mounts that category path.
	form := url.Values{"urls": {c.URL}, "category": {t.Category}, "tags": {r.OwnerTag}, "autoTMM": {"true"}}
	if r.Volume != "" {
		// qBittorrent 5.1's stop condition fetches metadata without beginning
		// the pack payload, unlike an indefinitely stopped metadata-less magnet.
		form.Set("stopped", "false")
		form.Set("stopCondition", "MetadataReceived")
	}
	req, _ := http.NewRequestWithContext(ctx, "POST", t.URL+"/api/v2/torrents/add", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, e := t.client.Do(req)
	if e != nil {
		return r, errors.New("torrent submission ambiguous")
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 100))
	if resp.StatusCode == 400 || resp.StatusCode == 401 || resp.StatusCode == 403 || (resp.StatusCode == 200 && strings.Contains(string(data), "Fails")) {
		return r, jobs.SafeFailure{Err: errors.New("torrent rejected")}
	}
	if resp.StatusCode != 200 {
		return r, errors.New("torrent submission outcome unresolved")
	}
	return r, nil
}
func (t *Torrent) Prepare(ctx context.Context, c jobs.Candidate, d string) (jobs.Receipt, error) {
	if !filepath.IsAbs(t.DownloadRoot) {
		return jobs.Receipt{}, errors.New("torrent payload mount is not configured")
	}
	var extra map[string]string
	json.Unmarshal(c.Extra, &extra)
	hash := strings.ToLower(extra["hash"])
	if u, e := url.Parse(c.URL); e == nil && u.Scheme == "magnet" {
		hash = strings.TrimPrefix(u.Query().Get("xt"), "urn:btih:")
	}
	if !hashPattern.MatchString(hash) {
		return jobs.Receipt{}, errors.New("stable torrent hash required")
	}
	return jobs.Receipt{ID: hash, State: "transferring", Format: c.Format, Volume: extra["volume"], OwnerTag: "arrdio-" + t.Instance + "-" + filepath.Base(filepath.Dir(d)) + "-" + filepath.Base(d)}, nil
}
func (t *Torrent) Poll(ctx context.Context, r jobs.Receipt) (jobs.Receipt, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if !hashPattern.MatchString(r.ID) {
		return r, jobs.ErrInvalid
	}
	if e := t.login(ctx); e != nil {
		return r, e
	}
	var info []struct {
		Progress    float64 `json:"progress"`
		ContentPath string  `json:"content_path"`
		State       string  `json:"state"`
		Tags        string  `json:"tags"`
	}
	if e := jsonCall(ctx, t.client, "GET", t.URL+"/api/v2/torrents/info?hashes="+r.ID, nil, nil, &info); e != nil {
		return r, e
	}
	if len(info) != 1 {
		return r, jobs.ErrUnresolvedTransfer
	}
	r.State = "transferring"
	if strings.HasPrefix(info[0].State, "paused") || strings.HasPrefix(info[0].State, "stopped") {
		r.State = "provider_wait"
	}
	var files []struct {
		Index    int     `json:"index"`
		Name     string  `json:"name"`
		Progress float64 `json:"progress"`
		Priority int     `json:"priority"`
		Size     int64   `json:"size"`
	}
	if e := jsonCall(ctx, t.client, "GET", t.URL+"/api/v2/torrents/files?hash="+r.ID, nil, nil, &files); e != nil {
		return r, e
	}
	if r.Volume != "" {
		all, selected, missing := []string{}, []string{}, []string{}
		for _, f := range files {
			all = append(all, strconv.Itoa(f.Index))
			if fileMatchesVolume(f.Name, r.Volume) {
				selected = append(selected, strconv.Itoa(f.Index))
				if f.Priority == 0 {
					missing = append(missing, strconv.Itoa(f.Index))
				}
			}
		}
		if len(files) == 0 {
			return r, nil
		}
		if len(selected) == 0 {
			return r, errors.New("requested volume not identified in torrent files")
		}
		owned := false
		for _, tag := range strings.Split(info[0].Tags, ",") {
			if strings.TrimSpace(tag) == r.OwnerTag {
				owned = true
			}
		}
		if owned && !r.SelectionDone {
			if e := t.form(ctx, "filePrio", url.Values{"hash": {r.ID}, "id": {strings.Join(all, "|")}, "priority": {"0"}}); e != nil {
				return r, e
			}
			if e := t.form(ctx, "filePrio", url.Values{"hash": {r.ID}, "id": {strings.Join(selected, "|")}, "priority": {"1"}}); e != nil {
				return r, e
			}
			if e := t.form(ctx, "start", url.Values{"hashes": {r.ID}}); e != nil {
				return r, e
			}
		} else if len(missing) > 0 {
			if e := t.form(ctx, "filePrio", url.Values{"hash": {r.ID}, "id": {strings.Join(missing, "|")}, "priority": {"1"}}); e != nil {
				return r, e
			}
		}
		if !r.SelectionDone || len(missing) > 0 {
			r.SelectionDone = true
			return r, nil
		}
	}
	paths := []string{}
	for _, f := range files {
		if r.Volume != "" && !fileMatchesVolume(f.Name, r.Volume) {
			continue
		}
		if f.Priority == 0 {
			continue
		}
		if f.Progress < 1 {
			return r, nil
		}
		path := filepath.Join(t.DownloadRoot, filepath.FromSlash(f.Name))
		rel, e := filepath.Rel(t.DownloadRoot, path)
		if e != nil || strings.HasPrefix(rel, "..") {
			return r, errors.New("unsafe torrent path")
		}
		if st, e := os.Stat(path); e != nil || st.Size() == 0 {
			return r, errors.New("completed payload not mounted")
		}
		if st, e := os.Stat(path); e != nil || (f.Size > 0 && st.Size() != f.Size) {
			return r, errors.New("torrent payload size mismatch")
		}
		paths = append(paths, path)
	}
	if len(paths) == 0 {
		return r, errors.New("empty torrent")
	}
	r.Paths = paths
	r.State = "verified"
	return r, nil
}
func (t *Torrent) form(ctx context.Context, action string, data url.Values) error {
	req, _ := http.NewRequestWithContext(ctx, "POST", t.URL+"/api/v2/torrents/"+action, strings.NewReader(data.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, e := t.client.Do(req)
	if e != nil {
		return errors.New("torrent control unavailable")
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return HTTPStatusError{resp.StatusCode}
	}
	return nil
}

// ABB is independent of Prowlarr. It runs for each missing book, not batch errors.
type ABB struct {
	URL        string
	Cookie     string
	Username   string
	Password   string
	mu         sync.Mutex
	client     *http.Client
	verifiedAt time.Time
	Torrent    *Torrent
}

func (a *ABB) Name() string { return "abb" }
func (a *ABB) Search(ctx context.Context, in jobs.Input, format string) ([]jobs.Candidate, error) {
	if format != "audio" || a.URL == "" {
		return nil, nil
	}
	base, e := url.Parse(a.URL)
	if e != nil {
		return nil, e
	}
	client, e := a.authenticatedClient(ctx)
	if e != nil {
		return nil, e
	}
	req, _ := http.NewRequestWithContext(ctx, "GET", a.URL+"/?s="+escape(strings.ToLower(in.Title))+"&tt=1", nil)
	req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; ARRDIO/2.0)")
	if a.Cookie != "" {
		req.Header.Set("Cookie", a.Cookie)
	}
	resp, e := client.Do(req)
	if e != nil {
		return nil, errors.New("ABB unavailable")
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, errors.New("ABB unavailable")
	}
	doc, e := html.Parse(io.LimitReader(resp.Body, 4<<20))
	if e != nil {
		return nil, e
	}
	out := []jobs.Candidate{}
	links := []string{}
	seen := map[string]bool{}
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode && n.Data == "a" {
			for _, attr := range n.Attr {
				if attr.Key == "href" && strings.Contains(attr.Val, "/abss/") {
					u, e := abbDetailURL(base, attr.Val)
					if e == nil {
						if len(links) < 8 && !seen[u.Path] {
							seen[u.Path] = true
							links = append(links, u.String())
						}
					}
				}
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(doc)
	for _, link := range links {
		req, _ := http.NewRequestWithContext(ctx, "GET", link, nil)
		req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; ARRDIO/2.0)")
		if a.Cookie != "" {
			req.Header.Set("Cookie", a.Cookie)
		}
		res, e := client.Do(req)
		if e != nil {
			continue
		}
		data, _ := io.ReadAll(io.LimitReader(res.Body, 4<<20))
		res.Body.Close()
		parsed, e := html.Parse(bytes.NewReader(data))
		if e != nil {
			continue
		}
		text := htmlText(parsed)
		m := regexp.MustCompile(`(?i)info\s*hash\s*:?\s*([a-f0-9]{40})`).FindStringSubmatch(text)
		if m == nil {
			raw := regexp.MustCompile(`(?i)urn:btih:([a-f0-9]{40})`).FindSubmatch(data)
			if raw != nil {
				m = []string{string(raw[0]), string(raw[1])}
			}
		}
		if m == nil {
			continue
		}
		hash := strings.ToLower(m[1])
		title := in.Title // Search page constrains query; title must still be confirmed from detail.
		tm := regexp.MustCompile(`(?is)<title>(.*?)</title>`).FindSubmatch(data)
		if tm == nil {
			continue
		}
		title = stdhtml.UnescapeString(string(tm[1]))
		author := abbField(text, `(?i)Written\s+by\s*:?[\s]*(.+?)\s+(?:Read\s+by|Narrated\s+by|Format\s*:)`)
		language := abbField(text, `(?i)Language\s*:\s*(.+?)\s+(?:Keywords\s*:|Shared\s+by\s*:)`)
		extension := strings.ToLower(abbField(text, `(?i)Format\s*:\s*(M4B|MP3|M4A|FLAC|AAC|OGG|OPUS)\b`))
		narrator := abbField(text, `(?i)(?:Read|Narrated)\s+by\s*:?\s*(.+?)\s+(?:Format\s*:|Bitrate\s*:)`)
		edition := ""
		if regexp.MustCompile(`(?i)\bUnabridged\b`).MatchString(text) {
			edition = "unabridged"
		} else if regexp.MustCompile(`(?i)\bAbridged\b`).MatchString(text) {
			edition = "abridged"
		}
		extra, _ := json.Marshal(map[string]string{"hash": hash})
		out = append(out, jobs.Candidate{ID: hash, Provider: a.Name(), Title: title, Author: author, Language: language, Narrator: narrator, Edition: edition, Extension: extension, Format: "audio", URL: "magnet:?xt=urn:btih:" + hash, Extra: extra})
	}
	return Rank(in, format, out), nil
}
func abbField(text, pattern string) string {
	match := regexp.MustCompile(pattern).FindStringSubmatch(text)
	if len(match) < 2 {
		return ""
	}
	return strings.TrimSpace(match[1])
}
func htmlText(n *html.Node) string {
	var text strings.Builder
	var walk func(*html.Node)
	walk = func(node *html.Node) {
		if node.Type == html.TextNode {
			text.WriteString(node.Data)
			text.WriteByte(' ')
		}
		for c := node.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(n)
	return text.String()
}
func (a *ABB) Acquire(ctx context.Context, c jobs.Candidate, d string) (jobs.Receipt, error) {
	if a.Torrent == nil {
		return jobs.Receipt{}, fmt.Errorf("torrent client not configured")
	}
	return a.Torrent.Add(ctx, c, d)
}
func (a *ABB) Prepare(ctx context.Context, c jobs.Candidate, d string) (jobs.Receipt, error) {
	if a.Torrent == nil {
		return jobs.Receipt{}, errors.New("torrent client not configured")
	}
	return a.Torrent.Prepare(ctx, c, d)
}
func (a *ABB) Reconcile(ctx context.Context, r jobs.Receipt) (jobs.Receipt, error) {
	return a.Torrent.Poll(ctx, r)
}
