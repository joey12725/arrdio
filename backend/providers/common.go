package providers

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"github.com/ashkaty/arrdio-api/jobs"
	"image"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

func sameOriginRedirect(req *http.Request, via []*http.Request) error {
	if len(via) > 5 || len(via) == 0 {
		return errors.New("redirect limit exceeded")
	}
	first := via[0].URL
	if req.URL.Scheme != first.Scheme || req.URL.Host != first.Host {
		return errors.New("cross-origin service redirect rejected")
	}
	return nil
}

var Client = &http.Client{Timeout: 45 * time.Second, CheckRedirect: sameOriginRedirect}

type HTTPStatusError struct{ Code int }

func (e HTTPStatusError) Error() string { return "service rejected request" }

func jsonCall(ctx context.Context, client *http.Client, method, url string, body io.Reader, headers map[string]string, out any) error {
	req, e := http.NewRequestWithContext(ctx, method, url, body)
	if e != nil {
		return e
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, e := client.Do(req)
	if e != nil {
		return errors.New("service transport unavailable")
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return HTTPStatusError{Code: resp.StatusCode}
	}
	return json.NewDecoder(io.LimitReader(resp.Body, 16<<20)).Decode(out)
}
func jsonPage(ctx context.Context, url string, body io.Reader, headers map[string]string, out any) (int, error) {
	req, e := http.NewRequestWithContext(ctx, "POST", url, body)
	if e != nil {
		return 0, e
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, e := Client.Do(req)
	if e != nil {
		return 0, errors.New("library transport unavailable")
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return 0, HTTPStatusError{resp.StatusCode}
	}
	var pagination struct {
		TotalPages int `json:"totalPages"`
	}
	if json.Unmarshal([]byte(resp.Header.Get("Pagination")), &pagination) != nil || pagination.TotalPages < 0 {
		return 0, errors.New("library pagination unavailable")
	}
	if e = json.NewDecoder(io.LimitReader(resp.Body, 16<<20)).Decode(out); e != nil {
		return 0, e
	}
	return pagination.TotalPages, nil
}

var words = regexp.MustCompile(`[\pL\pN]+`)

func tokens(s string) map[string]bool {
	out := map[string]bool{}
	for _, v := range words.FindAllString(strings.ToLower(s), -1) {
		if len(v) > 1 {
			out[v] = true
		}
	}
	return out
}
func overlap(a, b string) float64 {
	if strings.TrimSpace(a) != "" && strings.EqualFold(strings.TrimSpace(a), strings.TrimSpace(b)) {
		return 1
	}
	x, y := tokens(a), tokens(b)
	if len(x) == 0 {
		return 0
	}
	n := 0
	for v := range x {
		if y[v] {
			n++
		}
	}
	return float64(n) / float64(len(x))
}
func Rank(in jobs.Input, format string, candidates []jobs.Candidate) []jobs.Candidate {
	good := []jobs.Candidate{}
	for _, c := range candidates {
		if !in.Allows(c, format) {
			continue
		}
		plausible := overlap(in.Title, c.Title) >= 0.6
		if !plausible && in.Author != "" && c.Author != "" && overlap(in.Author, c.Author) >= 0.8 {
			if base, _, ok := strings.Cut(in.Title, ":"); ok {
				plausible = strings.EqualFold(strings.TrimSpace(base), strings.TrimSpace(c.Title))
			}
		}
		if c.Format != format || !plausible {
			continue
		}
		if !shortTitleMatches(in, c) {
			continue
		}
		if in.Author != "" && c.Author != "" && overlap(in.Author, c.Author) < 0.4 {
			continue
		}
		if in.Volume != "" && !coversVolume(c.Volume, in.Volume) {
			continue
		}
		good = append(good, c)
	}
	// Like the ebook search, an unconstrained request prefers English but still
	// accepts other languages. Unknown language is not treated as foreign.
	foreign := func(c jobs.Candidate) bool {
		return in.Constraints.Language == "" && c.Language != "" && !jobs.LanguageMatches("en", c.Language)
	}
	sort.SliceStable(good, func(i, j int) bool {
		a, b := good[i], good[j]
		if foreign(a) != foreign(b) {
			return !foreign(a)
		}
		if a.Seeders != nil && b.Seeders != nil && *a.Seeders != *b.Seeders {
			return *a.Seeders > *b.Seeders
		}
		if a.Seeders != nil && *a.Seeders > 0 && b.Seeders == nil {
			return true
		}
		if b.Seeders != nil && *b.Seeders > 0 && a.Seeders == nil {
			return false
		}
		return overlap(in.Title, a.Title) > overlap(in.Title, b.Title)
	})
	return good
}
func shortTitleMatches(in jobs.Input, candidate jobs.Candidate) bool {
	ignored := map[string]bool{"the": true, "a": true, "an": true, "of": true, "and": true, "by": true, "read": true, "narrated": true, "audiobook": true, "audiobooks": true, "audio": true, "book": true, "unabridged": true, "abridged": true, "english": true, "complete": true, "uncut": true, "m4b": true, "mp3": true, "m4a": true, "flac": true, "epub": true, "pdf": true, "cbz": true, "cbr": true}
	wanted := tokens(strings.SplitN(in.Title, ":", 2)[0])
	for token := range wanted {
		if ignored[token] {
			delete(wanted, token)
		}
	}
	if len(wanted) == 0 || len(wanted) > 2 {
		return true
	}
	core := strings.SplitN(candidate.Title, ":", 2)[0]
	core = regexp.MustCompile(`\([^)]*\)|\[[^]]*\]`).ReplaceAllString(core, "")
	core = volumePattern.ReplaceAllString(core, "")
	authors := tokens(in.Author)
	for token := range tokens(candidate.Author) {
		authors[token] = true
	}
	for token := range tokens(core) {
		if ignored[token] || authors[token] || wanted[token] {
			continue
		}
		if _, e := strconv.Atoi(token); e == nil {
			continue
		}
		return false
	}
	return true
}

var volumePattern = regexp.MustCompile(`(?i)(?:\bvol(?:ume)?\.?\s*|\bv\.?\s*)(\d+(?:\.\d+)?)(?:\s*[-–]\s*(?:v(?:ol(?:ume)?)?\.?\s*)?(\d+(?:\.\d+)?))?`)

func volumeCoverage(title string) string {
	matches := volumePattern.FindAllStringSubmatch(title, -1)
	if len(matches) == 0 {
		return ""
	}
	m := matches[len(matches)-1]
	if m[2] != "" {
		return m[1] + "-" + m[2]
	}
	return m[1]
}
func fileMatchesVolume(name, wanted string) bool {
	coverage := volumeCoverage(name)
	if strings.Contains(coverage, "-") {
		return false
	}
	return coversVolume(coverage, wanted)
}
func coversVolume(coverage, requested string) bool {
	if coverage == "" {
		return false
	}
	want, e := strconv.ParseFloat(requested, 64)
	if e != nil {
		return strings.EqualFold(coverage, requested)
	}
	parts := strings.Split(coverage, "-")
	start, e := strconv.ParseFloat(parts[0], 64)
	if e != nil {
		return false
	}
	if len(parts) == 1 {
		return start == want
	}
	end, e := strconv.ParseFloat(parts[1], 64)
	return e == nil && want >= start && want <= end
}
func audioFile(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".mp3", ".m4b", ".m4a", ".aac", ".flac", ".ogg", ".opus":
		return true
	}
	return false
}
func Validate(path string) error {
	f, e := os.Open(path)
	if e != nil {
		return e
	}
	defer f.Close()
	st, e := f.Stat()
	if e != nil || st.Size() == 0 {
		return errors.New("empty artifact")
	}
	head := make([]byte, 512)
	n, _ := f.Read(head)
	head = head[:n]
	lower := bytes.ToLower(bytes.TrimSpace(head))
	if bytes.HasPrefix(lower, []byte("<!doctype html")) || bytes.HasPrefix(lower, []byte("<html")) {
		return errors.New("HTML is not a book")
	}
	switch strings.ToLower(filepath.Ext(path)) {
	case ".epub", ".cbz":
		z, e := zip.OpenReader(path)
		if e != nil {
			return errors.New("corrupt archive")
		}
		defer z.Close()
		mimetype := false
		documents := 0
		images := 0
		var expanded uint64
		for _, entry := range z.File {
			expanded += entry.UncompressedSize64
			if expanded > 1<<30 {
				return errors.New("archive expanded size exceeds limit")
			}
			if entry.UncompressedSize64 > 256<<20 {
				return errors.New("oversized archive member")
			}
			if strings.HasPrefix(entry.Name, "/") || strings.Contains(entry.Name, "../") {
				return errors.New("unsafe archive member")
			}
			if strings.HasSuffix(entry.Name, ".xhtml") || strings.HasSuffix(entry.Name, ".html") {
				documents++
			}
			if strings.HasSuffix(strings.ToLower(entry.Name), ".jpg") || strings.HasSuffix(strings.ToLower(entry.Name), ".png") {
				images++
			}
			if entry.Name == "mimetype" {
				h, e := entry.Open()
				if e != nil {
					return e
				}
				data, e := io.ReadAll(io.LimitReader(h, 100))
				h.Close()
				mimetype = e == nil && string(data) == "application/epub+zip"
			}
			if !entry.FileInfo().IsDir() {
				member, e := entry.Open()
				if e != nil {
					return errors.New("corrupt archive member")
				}
				_, e = io.Copy(io.Discard, io.LimitReader(member, 256<<20+1))
				member.Close()
				if e != nil {
					return errors.New("archive checksum failed")
				}
			}
		}
		if strings.EqualFold(filepath.Ext(path), ".epub") && (!mimetype || documents == 0) {
			return errors.New("invalid EPUB structure")
		}
		if strings.EqualFold(filepath.Ext(path), ".cbz") && images == 0 {
			return errors.New("empty comic archive")
		}
	case ".pdf":
		if !bytes.HasPrefix(head, []byte("%PDF-")) || st.Size() < 1024 {
			return errors.New("invalid PDF")
		}
		tail := make([]byte, 1024)
		f.ReadAt(tail, st.Size()-1024)
		if !bytes.Contains(tail, []byte("%%EOF")) {
			return errors.New("truncated PDF")
		}
	case ".cbr":
		if !bytes.HasPrefix(head, []byte("Rar!\x1a\x07")) {
			return errors.New("invalid RAR signature")
		}
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		var listing bytes.Buffer
		command := exec.CommandContext(ctx, "bsdtar", "-tf", path)
		command.Stdout = &boundedWriter{Writer: &listing, Remaining: 1 << 20}
		if command.Run() != nil {
			return errors.New("CBR decoder unavailable or archive corrupt")
		}
		images := 0
		for _, name := range strings.Split(listing.String(), "\n") {
			name = strings.ToLower(name)
			if strings.HasSuffix(name, ".jpg") || strings.HasSuffix(name, ".jpeg") || strings.HasSuffix(name, ".png") || strings.HasSuffix(name, ".webp") {
				images++
			}
		}
		if images == 0 {
			return errors.New("comic archive contains no images")
		}
		command = exec.CommandContext(ctx, "bsdtar", "-xOf", path)
		command.Stdout = &boundedWriter{Writer: io.Discard, Remaining: 1 << 30}
		if command.Run() != nil {
			return errors.New("comic archive validation failed")
		}
	case ".jpg", ".jpeg", ".png":
		if st.Size() > 20<<20 {
			return errors.New("artwork exceeds limit")
		}
		f.Seek(0, 0)
		config, _, e := image.DecodeConfig(f)
		if e != nil || config.Width*config.Height > 24_000_000 {
			return errors.New("invalid artwork")
		}
	default:
		return errors.New("unsupported artifact requires media verification")
	}
	return nil
}

type boundedWriter struct {
	Writer    io.Writer
	Remaining int64
}

func (w *boundedWriter) Write(data []byte) (int, error) {
	if int64(len(data)) > w.Remaining {
		return 0, errors.New("decoded data limit exceeded")
	}
	n, e := w.Writer.Write(data)
	w.Remaining -= int64(n)
	return n, e
}
func atomicWrite(path string, data []byte) error {
	f, e := os.CreateTemp(filepath.Dir(path), ".arrdio-*")
	if e != nil {
		return e
	}
	defer os.Remove(f.Name())
	if _, e = f.Write(data); e != nil {
		f.Close()
		return e
	}
	if e = f.Sync(); e != nil {
		f.Close()
		return e
	}
	if e = f.Close(); e != nil {
		return e
	}
	return os.Rename(f.Name(), path)
}
func Catalog(ctx context.Context, query string) ([]jobs.Candidate, error) {
	var result struct {
		Docs []struct {
			Key      string   `json:"key"`
			Title    string   `json:"title"`
			Authors  []string `json:"author_name"`
			Cover    int      `json:"cover_i"`
			Subjects []string `json:"subject"`
		} `json:"docs"`
	}
	e := jsonCall(ctx, Client, "GET", "https://openlibrary.org/search.json?q="+escape(query)+"&limit=8", nil, nil, &result)
	if e != nil {
		return nil, e
	}
	out := []jobs.Candidate{}
	for _, d := range result.Docs {
		c := jobs.Candidate{ID: d.Key, Provider: "openlibrary", Title: d.Title, Author: strings.Join(d.Authors, ", ")}
		for _, subject := range d.Subjects {
			if strings.Contains(strings.ToLower(subject), "manga") {
				c.Kind = "manga"
				break
			}
		}
		if d.Cover > 0 {
			c.Cover = coverURL(d.Cover)
		}
		out = append(out, c)
	}
	return out, nil
}
func Enrich(ctx context.Context, in jobs.Input) (jobs.Candidate, error) {
	c := jobs.Candidate{ID: in.CatalogID}
	if c.ID == "" {
		results, e := Catalog(ctx, in.Title+" "+in.Author)
		if e != nil || len(results) == 0 {
			return c, e
		}
		c = results[0]
		best := -1.0
		found := false
		for _, candidate := range results {
			title := overlap(in.Title, candidate.Title)
			if title < 0.8 {
				continue
			}
			author := 1.0
			if in.Author != "" {
				author = overlap(in.Author, candidate.Author)
				if author < 0.7 {
					continue
				}
			}
			score := title + author
			if score > best {
				best = score
				c = candidate
				found = true
			}
		}
		if !found {
			return jobs.Candidate{}, errors.New("metadata identity mismatch")
		}
	}
	if !regexp.MustCompile(`^/works/OL[0-9]+W$`).MatchString(c.ID) {
		return c, nil
	}
	var work struct {
		Title       string          `json:"title"`
		Description json.RawMessage `json:"description"`
		Covers      []int           `json:"covers"`
	}
	if e := jsonCall(ctx, Client, "GET", "https://openlibrary.org"+c.ID+".json", nil, nil, &work); e != nil {
		return c, nil
	}
	if overlap(in.Title, work.Title) < 0.8 {
		return jobs.Candidate{}, errors.New("catalog work identity mismatch")
	}
	var description string
	if json.Unmarshal(work.Description, &description) != nil {
		var v struct {
			Value string `json:"value"`
		}
		json.Unmarshal(work.Description, &v)
		description = v.Value
	}
	if len(description) > 12000 {
		description = description[:12000]
	}
	c.Description = description
	if c.Cover == "" && len(work.Covers) > 0 {
		c.Cover = coverURL(work.Covers[0])
	}
	return c, nil
}
