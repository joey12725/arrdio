package providers

import (
	"bytes"
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/joey12725/arrdio/backend/jobs"
	"golang.org/x/net/html"
	stdhtml "html"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	pathpkg "path"
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
	mu       sync.Mutex
	fetched  map[string]fetchedTorrent
}

// fetchedTorrent keeps a .torrent between Prepare and Acquire so a private
// tracker records one grab per release rather than two.
type fetchedTorrent struct {
	data []byte
	at   time.Time
}

func (p *Prowlarr) Name() string { return "prowlarr" }
func categoryIDs(categories []Category, kind string) []int {
	out := []int{}
	for _, c := range categories {
		n := strings.ToLower(c.Name)
		ebook := (strings.Contains(n, "ebook") || strings.Contains(n, "e-book")) && !strings.Contains(n, "audio") && !strings.Contains(n, "comic")
		// Nyaa's Literature holds raws and other translations beside the
		// English releases; only English-translated ones fit a library request.
		foreign := strings.Contains(n, "raw") || strings.Contains(n, "non-english")
		matches := (kind == "audio" && (strings.Contains(n, "audiobook") || strings.Contains(n, "audio book"))) || (kind == "manga" && !foreign && (strings.Contains(n, "literature") || strings.Contains(n, "comics"))) || (kind == "ebook" && ebook)
		children := categoryIDs(c.Children, kind)
		// A parent searches all of its children, so a parent with matching
		// children is replaced by them (Literature -> English-translated).
		if matches && (len(c.Children) == 0 || len(children) == 0) {
			out = append(out, c.ID)
		}
		out = append(out, children...)
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

// searchKind is the indexer category family for a request. An ebook that is
// a manga or comic (kind manga, set when it is routed to a manga library) is
// searched among comics and literature, where CBZ releases live.
func searchKind(in jobs.Input, format string) string {
	if format == "ebook" && in.Kind == "manga" {
		return "manga"
	}
	return format
}

// ebookReadable lists the containers Kavita reads, by search kind. MOBI and
// AZW3 releases would place files the library cannot open.
var ebookReadable = map[string]map[string]bool{
	"ebook": {"epub": true, "pdf": true},
	"manga": {"epub": true, "pdf": true, "cbz": true, "cbr": true},
}

func (p *Prowlarr) Search(ctx context.Context, in jobs.Input, format string) ([]jobs.Candidate, error) {
	if p.URL == "" {
		return nil, nil
	}
	var indexers []prowlarrIndexer
	headers := map[string]string{"X-Api-Key": p.Key}
	if e := jsonCall(ctx, Client, "GET", p.URL+"/api/v1/indexer", nil, headers, &indexers); e != nil {
		return nil, e
	}
	// Indexers match words literally: a subtitle written with a straight
	// apostrophe misses a release titled "Winter’s End". When the full title
	// finds nothing usable, the main title (before the colon) is tried.
	// Every query word must match, so only the first of several credited
	// authors is sent ("Haruto Kimura and Sora Ito" found nothing; the
	// release says "Haruto Kimura, Sora Ito"). Ranking still uses all.
	author := primaryAuthor(in.Author)
	queries := []string{strings.Join(strings.Fields(in.Title+" "+author+" "+in.Volume), " ")}
	if base, _, ok := strings.Cut(in.Title, ":"); ok && strings.TrimSpace(base) != "" {
		queries = append(queries, strings.Join(strings.Fields(base+" "+author+" "+in.Volume), " "))
	}
	// Nyaa names releases "Series v05" or "Series v01-20" without authors, so
	// manga also searches the volume-tagged and bare series title; ranking
	// keeps only releases covering the requested volume.
	if searchKind(in, format) == "manga" {
		if n, e := strconv.Atoi(in.Volume); e == nil {
			queries = append(queries, fmt.Sprintf("%s v%02d", in.Title, n))
		}
		queries = append(queries, in.Title)
	}
	for _, query := range queries {
		ranked := p.search(ctx, indexers, headers, query, in, format)
		if len(ranked) > 0 {
			return ranked, nil
		}
	}
	return []jobs.Candidate{}, nil
}

var authorSeparator = regexp.MustCompile(`(?i)\s*(?:,|;|&|\band\b|\bwith\b)\s*`)

// primaryAuthor is the first credited author of a combined credit.
func primaryAuthor(author string) string {
	return strings.TrimSpace(authorSeparator.Split(author, 2)[0])
}

type prowlarrIndexer struct {
	ID           int    `json:"id"`
	Name         string `json:"name"`
	Enable       bool   `json:"enable"`
	Privacy      string `json:"privacy"`
	Capabilities struct {
		Categories []Category `json:"categories"`
	} `json:"capabilities"`
}

func (p *Prowlarr) search(ctx context.Context, indexers []prowlarrIndexer, headers map[string]string, query string, in jobs.Input, format string) []jobs.Candidate {
	out := []jobs.Candidate{}
	for _, indexer := range indexers {
		if !indexer.Enable {
			continue
		}
		kind := searchKind(in, format)
		// Book torrents come only from private trackers (MyAnonamouse):
		// public ebook uploads are often mislabeled or unsafe.
		if kind == "ebook" && indexer.Privacy != "private" {
			continue
		}
		cats := categoryIDs(indexer.Capabilities.Categories, kind)
		if len(cats) == 0 {
			continue
		}
		q := url.Values{"query": {query}, "type": {"search"}, "indexerIds": {strconv.Itoa(indexer.ID)}}
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
			if len(categoryIDs(v.Categories, kind)) == 0 {
				continue
			}
			link := v.MagnetURL
			if link == "" {
				link = v.DownloadURL
			}
			extra, _ := json.Marshal(map[string]string{"hash": v.InfoHash, "format": format, "volume": in.Volume})
			// Audiobook releases also sit in literature categories (Nyaa).
			if format != "audio" && strings.Contains(strings.ToLower(v.Title), "audiobook") {
				continue
			}
			author, language, extension := releaseTag(v.Title)
			if format == "ebook" && extension != "" && !ebookReadable[kind][extension] {
				continue
			}
			out = append(out, jobs.Candidate{ID: v.GUID, Provider: p.Name(), Title: v.Title, Author: author, Language: language, Extension: extension, Format: format, Volume: volumeCoverage(v.Title), Seeders: v.Seeders, URL: link, Extra: extra})
		}
	}
	return Rank(in, format, out)
}
func (p *Prowlarr) Acquire(ctx context.Context, c jobs.Candidate, d string) (jobs.Receipt, error) {
	if p.Torrent == nil {
		return jobs.Receipt{}, errors.New("torrent client not configured")
	}
	c, file, e := p.resolve(ctx, c)
	if e != nil {
		return jobs.Receipt{}, jobs.SafeFailure{Err: e}
	}
	return p.Torrent.add(ctx, c, d, file)
}
func (p *Prowlarr) Prepare(ctx context.Context, c jobs.Candidate, d string) (jobs.Receipt, error) {
	if p.Torrent == nil {
		return jobs.Receipt{}, errors.New("torrent client not configured")
	}
	c, _, e := p.resolve(ctx, c)
	if e != nil {
		return jobs.Receipt{}, e
	}
	return p.Torrent.Prepare(ctx, c, d)
}

var prowlarrDownloadPath = regexp.MustCompile(`(^|/)\d+/download$`)

// resolve gives a candidate a stable infohash before any client effect. Private
// indexers such as MyAnonamouse expose neither a hash nor a magnet, only
// Prowlarr's download proxy, which a VPN-namespaced client may not reach. The
// .torrent is fetched from Prowlarr's own origin (so its API key goes nowhere
// else) and returned for upload; a redirect to a magnet is used as the link.
func (p *Prowlarr) resolve(ctx context.Context, c jobs.Candidate) (jobs.Candidate, []byte, error) {
	extra := map[string]string{}
	json.Unmarshal(c.Extra, &extra)
	if hashPattern.MatchString(extra["hash"]) || strings.HasPrefix(c.URL, "magnet:") {
		return c, nil, nil
	}
	link, e := url.Parse(c.URL)
	base, be := url.Parse(p.URL)
	if e != nil || be != nil || !prowlarrDownloadPath.MatchString(link.Path) {
		return c, nil, errors.New("release has no infohash or Prowlarr download link")
	}
	link.Scheme, link.Host, link.User = base.Scheme, base.Host, nil
	p.mu.Lock()
	cached, ok := p.fetched[link.String()]
	p.mu.Unlock()
	if ok && time.Since(cached.at) < 10*time.Minute {
		if hash, e := infoHash(cached.data); e == nil {
			extra["hash"] = hash
			c.Extra, _ = json.Marshal(extra)
			return c, cached.data, nil
		}
	}
	client := &http.Client{Timeout: 45 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	req, _ := http.NewRequestWithContext(ctx, "GET", link.String(), nil)
	req.Header.Set("X-Api-Key", p.Key)
	resp, e := client.Do(req)
	if e != nil {
		return c, nil, errors.New("torrent file unavailable")
	}
	defer resp.Body.Close()
	if location := resp.Header.Get("Location"); resp.StatusCode >= 300 && resp.StatusCode < 400 && strings.HasPrefix(location, "magnet:") {
		c.URL = location
		return c, nil, nil
	}
	if resp.StatusCode != 200 {
		return c, nil, HTTPStatusError{Code: resp.StatusCode}
	}
	data, e := io.ReadAll(io.LimitReader(resp.Body, 10<<20+1))
	if e != nil || len(data) > 10<<20 {
		return c, nil, errors.New("torrent file unavailable")
	}
	hash, e := infoHash(data)
	if e != nil {
		return c, nil, e
	}
	extra["hash"] = hash
	c.Extra, _ = json.Marshal(extra)
	p.mu.Lock()
	if p.fetched == nil || len(p.fetched) > 64 {
		p.fetched = map[string]fetchedTorrent{}
	}
	p.fetched[link.String()] = fetchedTorrent{data, time.Now()}
	p.mu.Unlock()
	return c, data, nil
}

// infoHash is the SHA-1 of the raw bencoded info dictionary of a v1 torrent.
func infoHash(data []byte) (string, error) {
	invalid := errors.New("invalid torrent file")
	if len(data) == 0 || data[0] != 'd' {
		return "", invalid
	}
	for i := 1; i < len(data) && data[i] != 'e'; {
		keyEnd, e := bencodeEnd(data, i)
		if e != nil || data[i] < '0' || data[i] > '9' {
			return "", invalid
		}
		key := data[bytes.IndexByte(data[i:keyEnd], ':')+i+1 : keyEnd]
		valueEnd, e := bencodeEnd(data, keyEnd)
		if e != nil {
			return "", invalid
		}
		if string(key) == "info" && data[keyEnd] == 'd' {
			sum := sha1.Sum(data[keyEnd:valueEnd])
			return hex.EncodeToString(sum[:]), nil
		}
		i = valueEnd
	}
	return "", invalid
}

// bencodeEnd returns the index just past the bencoded value starting at i.
func bencodeEnd(data []byte, i int) (int, error) {
	invalid := errors.New("invalid bencoding")
	for depth, start := 0, i; i < len(data); {
		switch c := data[i]; {
		case c == 'i':
			end := bytes.IndexByte(data[i:], 'e')
			if end < 0 {
				return 0, invalid
			}
			i += end + 1
		case c == 'l' || c == 'd':
			depth++
			i++
			continue
		case c == 'e':
			if depth == 0 {
				return 0, invalid
			}
			depth--
			i++
		case c >= '0' && c <= '9':
			colon := bytes.IndexByte(data[i:], ':')
			if colon < 0 {
				return 0, invalid
			}
			n, e := strconv.Atoi(string(data[i : i+colon]))
			if e != nil || n < 0 || i+colon+1+n > len(data) {
				return 0, invalid
			}
			i += colon + 1 + n
		default:
			return 0, invalid
		}
		if depth == 0 && i > start {
			return i, nil
		}
	}
	return 0, invalid
}
func (p *Prowlarr) Reconcile(ctx context.Context, r jobs.Receipt) (jobs.Receipt, error) {
	return p.Torrent.Poll(ctx, r)
}

type Torrent struct {
	URL, Username, Password, Category string
	DownloadRoot                      string
	// ClientRoot is the client's own path for DownloadRoot (e.g. "/downloads").
	// When set, files resolve from the torrent's reported save or incomplete
	// path, so partially selected packs left in the incomplete folder are found.
	// When empty, DownloadRoot is the category save path.
	ClientRoot string
	Instance   string
	client     *http.Client
	mu         sync.Mutex
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

// torrentStall is how long a stalled download may go without any data before
// its release is abandoned for another candidate.
var torrentStall = 4 * time.Hour

var hashPattern = regexp.MustCompile(`^[a-fA-F0-9]{40}$`)

func (t *Torrent) Add(ctx context.Context, c jobs.Candidate, d string) (jobs.Receipt, error) {
	return t.add(ctx, c, d, nil)
}

// add submits a magnet/URL, or the given .torrent bytes when a release has no
// link the client can fetch itself.
func (t *Torrent) add(ctx context.Context, c jobs.Candidate, d string, file []byte) (jobs.Receipt, error) {
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
	var body io.Reader = strings.NewReader(form.Encode())
	contentType := "application/x-www-form-urlencoded"
	if file != nil {
		var buffer bytes.Buffer
		parts := multipart.NewWriter(&buffer)
		form.Del("urls")
		for k, values := range form {
			for _, v := range values {
				parts.WriteField(k, v)
			}
		}
		part, _ := parts.CreateFormFile("torrents", hash+".torrent")
		part.Write(file)
		parts.Close()
		body, contentType = &buffer, parts.FormDataContentType()
	}
	req, _ := http.NewRequestWithContext(ctx, "POST", t.URL+"/api/v2/torrents/add", body)
	req.Header.Set("Content-Type", contentType)
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

// localPath maps a torrent file to the payload mount. qBittorrent keeps a
// partially selected torrent in its incomplete folder even after the wanted
// files finish, so the base comes from where the content actually is.
func (t *Torrent) localPath(content, save, incomplete, name string) (string, error) {
	rel := name
	if t.ClientRoot != "" {
		base := save
		if incomplete != "" && (content == incomplete || strings.HasPrefix(content, strings.TrimRight(incomplete, "/")+"/")) {
			base = incomplete
		}
		root := strings.TrimRight(t.ClientRoot, "/") + "/"
		full := pathpkg.Join(base, name)
		if !strings.HasPrefix(full, root) {
			return "", errors.New("torrent payload is outside the mounted client root")
		}
		rel = strings.TrimPrefix(full, root)
	}
	local := filepath.Join(t.DownloadRoot, filepath.FromSlash(rel))
	if r, e := filepath.Rel(t.DownloadRoot, local); e != nil || r == ".." || strings.HasPrefix(r, "../") {
		return "", errors.New("unsafe torrent path")
	}
	return local, nil
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
		Progress     float64 `json:"progress"`
		ContentPath  string  `json:"content_path"`
		State        string  `json:"state"`
		Tags         string  `json:"tags"`
		AddedOn      int64   `json:"added_on"`
		LastActivity int64   `json:"last_activity"`
		SavePath     string  `json:"save_path"`
		DownloadPath string  `json:"download_path"`
	}
	if e := jsonCall(ctx, t.client, "GET", t.URL+"/api/v2/torrents/info?hashes="+r.ID, nil, nil, &info); e != nil {
		return r, e
	}
	if len(info) != 1 {
		return r, jobs.ErrUnresolvedTransfer
	}
	// A swarm that never delivers would otherwise hold the request forever.
	// The torrent is left in the client; the job moves to another candidate.
	if v := info[0]; v.Progress < 1 && (v.State == "stalledDL" || v.State == "metaDL") {
		if since := max(v.AddedOn, v.LastActivity); since > 0 && time.Since(time.Unix(since, 0)) > torrentStall {
			return r, jobs.RetryTransferFailure{Err: errors.New("torrent made no progress; trying another release")}
		}
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
			return r, jobs.UnusableCandidate{Err: errors.New("requested volume not identified in torrent files")}
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
		path, e := t.localPath(info[0].ContentPath, info[0].SavePath, info[0].DownloadPath, f.Name)
		if e != nil {
			return r, e
		}
		if st, e := os.Stat(path); e != nil || st.Size() == 0 {
			return r, fmt.Errorf("%w: completed payload not mounted", jobs.ErrPayloadPending)
		}
		if st, e := os.Stat(path); e != nil || (f.Size > 0 && st.Size() != f.Size) {
			return r, fmt.Errorf("%w: torrent payload size mismatch", jobs.ErrPayloadPending)
		}
		paths = append(paths, path)
	}
	if r.Format == "ebook" {
		if paths = readableEbooks(paths); len(paths) == 0 {
			return r, jobs.UnusableCandidate{Err: errors.New("no readable ebook file in torrent")}
		}
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
	details    map[string]abbDetail
	Torrent    *Torrent
}

type abbDetail struct {
	candidate *jobs.Candidate
	at        time.Time
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
						if len(links) < 5 && !seen[u.Path] {
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
		if c := a.detail(ctx, client, link); c != nil {
			out = append(out, *c)
		}
	}
	return Rank(in, format, out), nil
}

// detail parses one ABB release page. Parsed pages are cached for six hours so
// repeated searches do not refetch them; bursts of these requests got a client
// IP blocked by ABB during live testing.
func (a *ABB) detail(ctx context.Context, client *http.Client, link string) *jobs.Candidate {
	a.mu.Lock()
	if cached, ok := a.details[link]; ok && time.Since(cached.at) < 6*time.Hour {
		a.mu.Unlock()
		return cached.candidate
	}
	a.mu.Unlock()
	c, definitive := a.parseDetail(ctx, client, link)
	if !definitive {
		return c
	}
	a.mu.Lock()
	if a.details == nil || len(a.details) > 2000 {
		a.details = map[string]abbDetail{}
	}
	a.details[link] = abbDetail{c, time.Now()}
	a.mu.Unlock()
	return c
}

// parseDetail reports definitive=false for transport or HTTP failures, which
// must not be cached as "no release".
func (a *ABB) parseDetail(ctx context.Context, client *http.Client, link string) (*jobs.Candidate, bool) {
	req, _ := http.NewRequestWithContext(ctx, "GET", link, nil)
	req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; ARRDIO/2.0)")
	if a.Cookie != "" {
		req.Header.Set("Cookie", a.Cookie)
	}
	res, e := client.Do(req)
	if e != nil {
		return nil, false
	}
	data, e := io.ReadAll(io.LimitReader(res.Body, 4<<20))
	res.Body.Close()
	if e != nil || res.StatusCode != 200 {
		return nil, false
	}
	parsed, e := html.Parse(bytes.NewReader(data))
	if e != nil {
		return nil, true
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
		return nil, true
	}
	hash := strings.ToLower(m[1])
	title := "" // Confirmed from the detail page, never assumed from the query.
	tm := regexp.MustCompile(`(?is)<title>(.*?)</title>`).FindSubmatch(data)
	if tm == nil {
		return nil, true
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
	return &jobs.Candidate{ID: hash, Provider: a.Name(), Title: title, Author: author, Language: language, Narrator: narrator, Edition: edition, Extension: extension, Format: "audio", URL: "magnet:?xt=urn:btih:" + hash, Extra: extra}, true
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

// readableEbooks keeps the best container an ebook torrent carries: book
// releases often bundle EPUB, MOBI, AZW3 and PDF copies of one work, and
// placing them all would show the book several times or not at all.
func readableEbooks(paths []string) []string {
	best, keep := -1, []string{}
	for _, path := range paths {
		ext := strings.TrimPrefix(strings.ToLower(filepath.Ext(path)), ".")
		if !ebookReadable["manga"][ext] {
			continue
		}
		rank := map[string]int{"epub": 0, "cbz": 1, "cbr": 2, "pdf": 3}[ext]
		switch {
		case best < 0 || rank < best:
			best, keep = rank, []string{path}
		case rank == best:
			keep = append(keep, path)
		}
	}
	return keep
}
