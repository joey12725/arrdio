package providers

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/ashkaty/arrdio-api/jobs"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"
)

type Route struct {
	Category  string `json:"category"`
	Format    string `json:"format"`
	Service   string `json:"service"`
	LibraryID string `json:"library_id"`
	Path      string `json:"path"`
}
type Libraries struct {
	ABSURL, ABSToken, KavitaURL, KavitaToken string
	ABSPublicURL, KavitaPublicURL            string
	KavitaKey                                string
	Routes                                   []Route
	mu                                       sync.Mutex
	cache                                    map[string]librarySnapshot
}
type libraryItem struct {
	Title, Author, Volume, ID, URL, Format string
	Evidence                               jobs.Candidate
}
type librarySnapshot struct {
	At    time.Time
	Items []libraryItem
}

func (l *Libraries) index(ctx context.Context, route Route) ([]libraryItem, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	key := libraryCacheKey(route)
	if l.cache == nil {
		l.cache = map[string]librarySnapshot{}
	}
	if s, ok := l.cache[key]; ok && time.Since(s.At) < time.Minute {
		return s.Items, nil
	}
	items := []libraryItem{}
	switch route.Service {
	case "audiobookshelf":
		if l.ABSURL == "" || l.ABSToken == "" {
			return nil, errors.New("ABS unavailable")
		}
		fetched := 0
		seen := map[string]bool{}
		for page := 0; page < 10000; page++ {
			var result struct {
				Total   *int `json:"total"`
				Results []struct {
					ID    string `json:"id"`
					Media struct {
						Metadata struct {
							Title        string `json:"title"`
							AuthorName   string `json:"authorName"`
							Language     string `json:"language"`
							NarratorName string `json:"narratorName"`
							Edition      string `json:"edition"`
						} `json:"metadata"`
						NumAudioFiles int             `json:"numAudioFiles"`
						EbookFile     json.RawMessage `json:"ebookFile"`
						AudioFiles    []struct {
							Metadata struct {
								Ext string `json:"ext"`
							} `json:"metadata"`
						} `json:"audioFiles"`
					} `json:"media"`
				} `json:"results"`
			}
			e := jsonCall(ctx, Client, "GET", fmt.Sprintf("%s/api/libraries/%s/items?limit=200&page=%d&expanded=1", l.ABSURL, route.LibraryID, page), nil, map[string]string{"Authorization": "Bearer " + l.ABSToken}, &result)
			if e != nil {
				return nil, e
			}
			for _, v := range result.Results {
				if seen[v.ID] {
					return nil, errors.New("library pagination changed during indexing")
				}
				seen[v.ID] = true
				public := l.ABSPublicURL
				if public == "" {
					public = l.ABSURL
				}
				base := libraryItem{Title: v.Media.Metadata.Title, Author: v.Media.Metadata.AuthorName, ID: v.ID, URL: public + "/item/" + v.ID}
				base.Evidence = jobs.Candidate{Language: v.Media.Metadata.Language, Narrator: v.Media.Metadata.NarratorName, Edition: v.Media.Metadata.Edition}
				if v.Media.NumAudioFiles > 0 {
					base.Format = "audio"
					if len(v.Media.AudioFiles) > 0 {
						extension := v.Media.AudioFiles[0].Metadata.Ext
						consistent := true
						for _, file := range v.Media.AudioFiles {
							if file.Metadata.Ext != extension {
								consistent = false
							}
						}
						if consistent {
							base.Evidence.Extension = strings.TrimPrefix(extension, ".")
						}
					}
					items = append(items, base)
				}
				if len(v.Media.EbookFile) > 0 && string(v.Media.EbookFile) != "null" {
					base.Format = "ebook"
					base.Evidence.Extension = ""
					var file struct {
						Metadata struct {
							Ext string `json:"ext"`
						} `json:"metadata"`
					}
					if json.Unmarshal(v.Media.EbookFile, &file) == nil {
						base.Evidence.Extension = strings.TrimPrefix(file.Metadata.Ext, ".")
					}
					items = append(items, base)
				}
			}
			if result.Total == nil || *result.Total > 2_000_000 {
				return nil, errors.New("library item count unavailable or exceeds bounded index")
			}
			fetched += len(result.Results)
			if len(result.Results) == 0 && fetched < *result.Total {
				return nil, errors.New("incomplete library index")
			}
			if fetched >= *result.Total {
				break
			}
			if page == 9999 {
				return nil, errors.New("library index exceeded page budget")
			}
		}
	case "kavita":
		if l.KavitaToken == "" && l.KavitaKey != "" {
			var auth struct {
				Token string `json:"token"`
			}
			e := jsonCall(ctx, Client, "POST", l.KavitaURL+"/api/Plugin/authenticate?apiKey="+escape(l.KavitaKey)+"&pluginName=arrdio", nil, nil, &auth)
			if e != nil {
				return nil, e
			}
			l.KavitaToken = auth.Token
		}
		if l.KavitaURL == "" || l.KavitaToken == "" {
			return nil, errors.New("Kavita unavailable")
		}
		for page := 1; page <= 1000; page++ {
			var result []struct {
				ID           int    `json:"id"`
				Name         string `json:"name"`
				OriginalName string `json:"originalName"`
				Format       int    `json:"format"`
			}
			body, _ := json.Marshal(map[string]any{"statements": []any{map[string]any{"comparison": 0, "field": 19, "value": route.LibraryID}}, "combination": 1, "sortOptions": map[string]any{"sortField": 1, "isAscending": true}, "limitTo": 0})
			totalPages, e := jsonPage(ctx, fmt.Sprintf("%s/api/Series/v2?PageNumber=%d&PageSize=5000", l.KavitaURL, page), bytes.NewReader(body), map[string]string{"Authorization": "Bearer " + l.KavitaToken, "Content-Type": "application/json"}, &result)
			if e != nil {
				var status HTTPStatusError
				if errors.As(e, &status) && (status.Code == 401 || status.Code == 403) {
					l.KavitaToken = ""
				}
				return nil, e
			}
			for _, v := range result {
				public := l.KavitaPublicURL
				if public == "" {
					public = l.KavitaURL
				}
				items = append(items, libraryItem{Title: v.Name, ID: fmt.Sprint(v.ID), URL: fmt.Sprintf("%s/library/%s/series/%d", public, route.LibraryID, v.ID), Format: route.Format})
			}
			if totalPages > 1000 {
				return nil, errors.New("library pagination exceeds bounded index")
			}
			if page >= totalPages {
				break
			}
		}
	default:
		return nil, errors.New("unknown library service")
	}
	l.cache[key] = librarySnapshot{time.Now(), items}
	return items, nil
}
func (l *Libraries) Presence(ctx context.Context, in jobs.Input, format string) ([]jobs.Presence, error) {
	return l.presence(ctx, in, format, "")
}
func (l *Libraries) presence(ctx context.Context, in jobs.Input, format, category string) ([]jobs.Presence, error) {
	out := []jobs.Presence{}
	checked := false
	var last error
	for _, route := range l.Routes {
		if route.Format != format || (category != "" && route.Category != category) {
			continue
		}
		checked = true
		p := jobs.Presence{Service: route.Service, State: "absent"}
		items, e := l.index(ctx, route)
		if e != nil {
			p.State = "unknown"
			last = e
		} else {
			for _, item := range items {
				if item.Format != format {
					continue
				}
				if overlap(in.Title, item.Title) >= 0.95 && (in.Author == "" || item.Author == "" || authorOverlap(in.Author, item.Author) >= 0.7) {
					if route.Service != "kavita" {
						state := in.RequirementState(item.Evidence, format)
						if state == "mismatch" {
							continue
						}
						if state == "unknown" {
							p.State = "unknown"
							last = errors.New("library metadata does not verify the explicit requirements")
							continue
						}
					}
					if route.Service == "kavita" {
						present, e := l.kavitaItem(ctx, item, in, format)
						if e != nil {
							p.State = "unknown"
							last = e
							continue
						}
						if !present {
							continue
						}
					}
					p.State = "available"
					p.ItemID = item.ID
					p.URL = item.URL
					break
				}
			}
			if p.State == "absent" && route.Service == "kavita" && format == "ebook" {
				if item, e := l.kavitaBookSearch(ctx, route, in); e != nil {
					p.State = "unknown"
					last = e
				} else if item != nil {
					p.State = "available"
					p.ItemID = item.ID
					p.URL = item.URL
				}
			}
		}
		out = append(out, p)
	}
	if !checked {
		return []jobs.Presence{{Service: "unconfigured", State: "unknown"}}, errors.New("no destination route")
	}
	for _, p := range out {
		if p.State == "available" {
			return out, nil
		}
	}
	return out, last
}

// kavitaBookSearch finds a book Kavita files under another series name
// ("The Bride of Lammermoor" inside "The Waverley Novels"), which the series
// index cannot see, by its chapter title, writers and file type.
func (l *Libraries) kavitaBookSearch(ctx context.Context, route Route, in jobs.Input) (*libraryItem, error) {
	l.mu.Lock()
	headers := map[string]string{"Authorization": "Bearer " + l.KavitaToken}
	l.mu.Unlock()
	var found struct {
		Chapters []struct {
			TitleName string `json:"titleName"`
			VolumeID  int    `json:"volumeId"`
			Files     []struct {
				Path string `json:"filePath"`
			} `json:"files"`
			Writers []struct {
				Name string `json:"name"`
			} `json:"writers"`
		} `json:"chapters"`
	}
	query := strings.TrimSpace(strings.SplitN(in.Title, ":", 2)[0])
	if e := l.kavitaRead(ctx, l.KavitaURL+"/api/Search/search?queryString="+escape(query), headers, &found); e != nil {
		return nil, e
	}
	for _, chapter := range found.Chapters {
		if overlap(in.Title, chapter.TitleName) < 0.95 || len(chapter.Files) == 0 {
			continue
		}
		if in.Author != "" {
			match := len(chapter.Writers) == 0
			for _, w := range chapter.Writers {
				if authorOverlap(in.Author, w.Name) >= 0.7 {
					match = true
				}
			}
			if !match {
				continue
			}
		}
		ebook := false
		for _, f := range chapter.Files {
			ext := strings.ToLower(filepath.Ext(f.Path))
			evidence := jobs.Candidate{Extension: strings.TrimPrefix(ext, ".")}
			if (ext == ".epub" || ext == ".pdf" || ext == ".azw3" || ext == ".mobi") && in.RequirementState(evidence, "ebook") == "match" {
				ebook = true
			}
		}
		if !ebook {
			continue
		}
		var volume struct {
			SeriesID int `json:"seriesId"`
		}
		if e := l.kavitaRead(ctx, fmt.Sprintf("%s/api/Volume?volumeId=%d", l.KavitaURL, chapter.VolumeID), headers, &volume); e != nil {
			return nil, e
		}
		var series struct {
			LibraryID int `json:"libraryId"`
		}
		if e := l.kavitaRead(ctx, fmt.Sprintf("%s/api/Series/%d", l.KavitaURL, volume.SeriesID), headers, &series); e != nil {
			return nil, e
		}
		if fmt.Sprint(series.LibraryID) != route.LibraryID {
			continue
		}
		public := l.KavitaPublicURL
		if public == "" {
			public = l.KavitaURL
		}
		return &libraryItem{Title: chapter.TitleName, ID: fmt.Sprint(volume.SeriesID), URL: fmt.Sprintf("%s/library/%s/series/%d", public, route.LibraryID, volume.SeriesID), Format: "ebook"}, nil
	}
	return nil, nil
}
func (l *Libraries) kavitaRead(ctx context.Context, url string, headers map[string]string, out any) error {
	e := jsonCall(ctx, Client, "GET", url, nil, headers, out)
	var status HTTPStatusError
	if errors.As(e, &status) && (status.Code == 401 || status.Code == 403) {
		l.mu.Lock()
		l.KavitaToken = ""
		l.cache = map[string]librarySnapshot{}
		l.mu.Unlock()
	}
	return e
}
func (l *Libraries) kavitaItem(ctx context.Context, item libraryItem, in jobs.Input, format string) (bool, error) {
	l.mu.Lock()
	token := l.KavitaToken
	l.mu.Unlock()
	headers := map[string]string{"Authorization": "Bearer " + token}
	evidence := jobs.Candidate{}
	if in.Author != "" || !in.Constraints.Empty() {
		var meta struct {
			Language string `json:"language"`
			Edition  string `json:"edition"`
			Writers  []struct {
				Name string `json:"name"`
			} `json:"writers"`
		}
		if e := l.kavitaRead(ctx, l.KavitaURL+"/api/Series/metadata?seriesId="+escape(item.ID), headers, &meta); e != nil {
			return false, e
		}
		evidence.Language = meta.Language
		evidence.Edition = meta.Edition
		if in.Author != "" && len(meta.Writers) > 0 {
			match := false
			for _, w := range meta.Writers {
				if authorOverlap(in.Author, w.Name) >= 0.7 {
					match = true
				}
			}
			if !match {
				return false, nil
			}
		}
	}
	var volumes []struct {
		Name      string  `json:"name"`
		MinNumber float64 `json:"minNumber"`
		MaxNumber float64 `json:"maxNumber"`
		Chapters  []struct {
			Number string `json:"number"`
			Files  []struct {
				Path string `json:"filePath"`
			} `json:"files"`
		} `json:"chapters"`
	}
	if e := l.kavitaRead(ctx, l.KavitaURL+"/api/Series/volumes?seriesId="+escape(item.ID), headers, &volumes); e != nil {
		return false, e
	}
	unknown := false
	for _, v := range volumes {
		coverage := volumeCoverage(v.Name)
		if coverage == "" {
			coverage = v.Name
		}
		if _, e := strconv.ParseFloat(coverage, 64); e != nil && v.MinNumber > 0 {
			coverage = strconv.FormatFloat(v.MinNumber, 'f', -1, 64)
		}
		if in.Volume != "" && !coversVolume(coverage, in.Volume) {
			continue
		}
		for _, chapter := range v.Chapters {
			for _, f := range chapter.Files {
				ext := strings.ToLower(filepath.Ext(f.Path))
				evidence.Extension = strings.TrimPrefix(ext, ".")
				state := in.RequirementState(evidence, format)
				if state == "mismatch" {
					continue
				}
				if state == "unknown" {
					unknown = true
					continue
				}
				if (format == "ebook" && (ext == ".epub" || ext == ".pdf" || ext == ".azw3" || ext == ".mobi")) || (format == "manga" && (ext == ".cbz" || ext == ".cbr" || ext == ".pdf" || ext == ".epub")) {
					return true, nil
				}
			}
		}
	}
	if unknown {
		return false, errors.New("library metadata does not establish the explicit requirements")
	}
	return false, nil
}
func (l *Libraries) ValidateRoute(ctx context.Context, in jobs.Input, format, category string) error {
	for _, route := range l.Routes {
		if route.Format != format || (category != "" && route.Category != category) {
			continue
		}
		if !filepath.IsAbs(route.Path) {
			return errors.New("destination must be absolute")
		}
		if e := os.MkdirAll(route.Path, 0775); e != nil {
			return e
		}
		probe, e := os.CreateTemp(route.Path, ".arrdio-write-check-*")
		if e != nil {
			return e
		}
		name := probe.Name()
		probe.Close()
		return os.Remove(name)
	}
	return errors.New("no compatible route")
}

// safePart makes one path component. It keeps the punctuation the libraries
// already use ("D. H. Lawrence", "Liam O'Flaherty", "Winter's End") and drops
// separators and characters Windows/SMB clients cannot open. Leading dots
// (hidden files, "..") and trailing dots are trimmed.
func safePart(s string) string {
	s = strings.NewReplacer("\u2019", "'", "\u2018", "'").Replace(s)
	var b strings.Builder
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsNumber(r) || strings.ContainsRune(".'-,&()!", r) {
			b.WriteRune(r)
		} else {
			b.WriteRune(' ')
		}
	}
	name := strings.Trim(strings.Join(strings.Fields(b.String()), " "), ". ")
	if !words.MatchString(name) {
		name = ""
	}
	if name == "" {
		name = "Unknown"
	}
	for len([]byte(name)) > 120 {
		name = name[:len(name)-1]
	}
	return strings.ToValidUTF8(name, "")
}
func digestFile(path string) (string, error) {
	return digestFileContext(context.Background(), path)
}

type contextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r contextReader) Read(data []byte) (int, error) {
	if e := r.ctx.Err(); e != nil {
		return 0, e
	}
	return r.reader.Read(data)
}
func digestFileContext(ctx context.Context, path string) (string, error) {
	f, e := os.Open(path)
	if e != nil {
		return "", e
	}
	defer f.Close()
	h := sha256.New()
	_, e = io.Copy(h, contextReader{ctx, f})
	return hex.EncodeToString(h.Sum(nil)), e
}
func copyVerified(src, dst string) error {
	return copyVerifiedContext(context.Background(), src, dst)
}
func copyVerifiedContext(ctx context.Context, src, dst string) error {
	if e := jobs.CheckImport(ctx); e != nil {
		return e
	}
	info, e := os.Lstat(src)
	if e != nil {
		return e
	}
	if !info.Mode().IsRegular() {
		return errors.New("source must be a regular file")
	}
	if e := ValidateMedia(src); e != nil {
		return e
	}
	hash, e := digestFileContext(ctx, src)
	if e != nil {
		return e
	}
	hooks := jobs.GetImportHooks(ctx)
	var owned jobs.OwnedPlacement
	if hooks != nil && hooks.Lookup != nil {
		owned, e = hooks.Lookup(dst)
		if e != nil {
			return e
		}
	}
	old := ""
	if info, e := os.Lstat(dst); e == nil {
		if !info.Mode().IsRegular() {
			return errors.New("destination is not a regular file")
		}
		old, e = digestFileContext(ctx, dst)
		if e != nil {
			return e
		}
	} else if !os.IsNotExist(e) {
		return e
	}
	if old == hash {
		if hooks != nil && owned.Pending == hash && hooks.Commit != nil {
			return hooks.Commit(dst, hash)
		}
		return nil
	}
	if old != "" && (hooks == nil || !hooks.Replace || owned.Digest != old || protectedPlacement(hooks, dst)) {
		return jobs.ErrPlacementConflict
	}
	if hooks != nil && hooks.Begin != nil {
		if e = hooks.Begin(src, dst, hash); e != nil {
			return e
		}
	}
	in, e := os.Open(src)
	if e != nil {
		return e
	}
	defer in.Close()
	tmp, e := os.CreateTemp(filepath.Dir(dst), ".arrdio-import-*")
	if e != nil {
		return e
	}
	defer os.Remove(tmp.Name())
	if _, e = io.Copy(tmp, contextReader{ctx, in}); e != nil {
		tmp.Close()
		return e
	}
	if e = tmp.Sync(); e != nil {
		tmp.Close()
		return e
	}
	tmp.Close()
	if got, e := digestFileContext(ctx, tmp.Name()); e != nil || got != hash {
		return errors.New("import copy mismatch")
	}
	if e = os.Chmod(tmp.Name(), 0666); e != nil {
		return e
	}
	if e = ownPlacement(tmp.Name(), 0666); e != nil {
		return e
	}
	if e = jobs.CheckImport(ctx); e != nil {
		return e
	}
	current, checkErr := digestFileContext(ctx, dst)
	if checkErr != nil && !os.IsNotExist(checkErr) {
		return checkErr
	}
	if current != old {
		return jobs.ErrPlacementConflict
	}
	if e = jobs.CheckImport(ctx); e != nil {
		return e
	}
	if e = os.Rename(tmp.Name(), dst); e != nil {
		return e
	}
	if hooks != nil && hooks.Commit != nil {
		return hooks.Commit(dst, hash)
	}
	return nil
}
func (l *Libraries) Import(ctx context.Context, in jobs.Input, format string, paths []string, category string) ([]jobs.Presence, error) {
	var route *Route
	for i := range l.Routes {
		v := &l.Routes[i]
		if v.Format == format && (category == "" || v.Category == category) {
			route = v
			break
		}
	}
	if route == nil {
		return nil, errors.New("route missing")
	}
	if len(paths) == 0 {
		return nil, errors.New("no verified files")
	}
	if e := jobs.CheckImport(ctx); e != nil {
		return nil, e
	}
	// Manga libraries group by series folder ("Manga/Green Hour/…"), books
	// and audiobooks by author and title.
	parts := []string{safePart(in.Author), safePart(in.Title + " " + in.Volume)}
	if format == "manga" {
		parts = []string{safePart(in.Title)}
	}
	folder, e := importFolder(route.Path, parts...)
	if e != nil {
		return nil, e
	}
	// A copy interrupted by a restart skips its deferred cleanup and leaves a
	// hidden partial file in the library folder. The lane imports one job at
	// a time, so any temp file here belongs to an earlier, dead attempt.
	if stale, _ := filepath.Glob(filepath.Join(folder, ".arrdio-import-*")); len(stale) > 0 {
		for _, old := range stale {
			os.Remove(old)
		}
	}
	ordered := append([]string(nil), paths...)
	if hooks := jobs.GetImportHooks(ctx); hooks == nil || !hooks.Ordered {
		sort.Slice(ordered, func(i, j int) bool { return naturalKey(ordered[i]) < naturalKey(ordered[j]) })
	}
	content := 0
	for _, src := range ordered {
		if (format == "audio" && audioFile(src)) || (format != "audio" && libraryExtension(src)) {
			content++
		}
	}
	imported := 0
	destinations := map[string]bool{}
	for index, src := range ordered {
		extension := strings.ToLower(filepath.Ext(src))
		artwork := strings.EqualFold(filepath.Base(src), "cover.jpg") || strings.EqualFold(filepath.Base(src), "folder.jpg") || strings.EqualFold(filepath.Base(src), "cover.png")
		if format == "audio" && !audioFile(src) && !artwork {
			continue
		}
		if format != "audio" && !libraryExtension(src) {
			continue
		}
		name := safePart(strings.TrimSuffix(filepath.Base(src), filepath.Ext(src)))
		if !artwork {
			name = libraryName(in, format, name, index, content)
		}
		dst := filepath.Join(folder, name+extension)
		if e := copyVerifiedContext(ctx, src, dst); e != nil {
			return nil, e
		}
		destinations[dst] = true
		if !artwork {
			imported++
		}
	}
	if imported == 0 {
		return nil, errors.New("no supported representation files")
	}
	hooks := jobs.GetImportHooks(ctx)
	if hooks != nil && hooks.Replace && hooks.Owned != nil {
		owned, e := hooks.Owned()
		if e != nil {
			return nil, e
		}
		for _, prior := range owned {
			if destinations[prior.Path] {
				continue
			}
			if e = jobs.CheckImport(ctx); e != nil {
				return nil, e
			}
			if protectedPlacement(hooks, prior.Path) {
				return nil, jobs.ErrPlacementConflict
			}
			hash, e := digestFileContext(ctx, prior.Path)
			if os.IsNotExist(e) {
				if e = hooks.Retired(prior.Path); e != nil {
					return nil, e
				}
				continue
			}
			if e != nil || hash != prior.Digest {
				return nil, jobs.ErrPlacementConflict
			}
			if e = jobs.CheckImport(ctx); e != nil {
				return nil, e
			}
			if e = os.Remove(prior.Path); e != nil {
				return nil, e
			}
			if e = hooks.Retired(prior.Path); e != nil {
				return nil, e
			}
		}
	}
	if e := jobs.CheckImport(ctx); e != nil {
		return nil, e
	}
	refreshKey := route.Service + ":" + route.LibraryID
	if hooks != nil && hooks.Dirty != nil {
		if e = hooks.Dirty(refreshKey); e != nil {
			return nil, e
		}
		requested, e := hooks.ClaimRefresh(refreshKey)
		if e != nil {
			return nil, e
		}
		if requested {
			refreshErr := l.Refresh(ctx, format, route.Category)
			if e = hooks.FinishRefresh(refreshKey, refreshErr == nil); e != nil {
				return nil, e
			}
		}
	} else {
		_ = l.Refresh(ctx, format, route.Category)
	}
	// A requested scan is not completion; read back through the same shared auth
	// instance, scoped to the actual destination category.
	l.mu.Lock()
	l.invalidateLibrary(*route)
	l.mu.Unlock()
	return l.presence(ctx, in, format, route.Category)
}
func libraryExtension(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".epub", ".pdf", ".cbz", ".cbr":
		return true
	}
	return false
}

// libraryName names a placed file the way the libraries read it. Kavita takes a
// PDF's or comic's series from its file name, so "book.pdf" would never match
// the request on readback. A single file is named for the request; multi-file
// sets keep their source names (numbered, for audio track order).
func libraryName(in jobs.Input, format, source string, index, count int) string {
	if count > 1 {
		if format == "audio" {
			return fmt.Sprintf("%04d %s", index+1, source)
		}
		return source
	}
	switch format {
	case "manga":
		volume := in.Volume
		if n, e := strconv.Atoi(volume); e == nil {
			volume = fmt.Sprintf("%02d", n)
		}
		if volume == "" {
			return safePart(in.Title)
		}
		return safePart(in.Title + " v" + volume)
	case "ebook":
		if strings.TrimSpace(in.Author) != "" {
			return safePart(in.Title) + " (" + safePart(in.Author) + ")"
		}
	}
	return safePart(in.Title)
}

// LibraryOwner, when set, owns every file and folder ARRDIO creates in a library.
// Shared media is usually nobody:users while its existing author folders were
// created by root services, so the process runs as root and hands its own
// placements to the library owner (files 0666, folders 0777). Existing folders
// are never changed.
var LibraryOwner *struct{ UID, GID int }

// ParseLibraryOwner reads "uid:gid".
func ParseLibraryOwner(value string) error {
	var uid, gid int
	if _, e := fmt.Sscanf(value, "%d:%d", &uid, &gid); e != nil || uid < 0 || gid < 0 || fmt.Sprintf("%d:%d", uid, gid) != value {
		return errors.New("library owner must be uid:gid")
	}
	LibraryOwner = &struct{ UID, GID int }{uid, gid}
	return nil
}

func ownPlacement(path string, mode os.FileMode) error {
	if LibraryOwner == nil {
		return nil
	}
	if e := os.Chown(path, LibraryOwner.UID, LibraryOwner.GID); e != nil {
		return e
	}
	return os.Chmod(path, mode)
}

// matchingFolder finds an existing folder in dir that names the same author,
// title or series as part once case and punctuation are ignored ("L. M. Montgomery"
// for "L M Montgomery", "Elowen - Past Winter's End" for "Elowen Past
// Winter's End"), the way Kavita groups series. The fullest one wins.
func matchingFolder(dir, part string) string {
	key := func(s string) string { return strings.Join(words.FindAllString(strings.ToLower(s), -1), "") }
	want := key(part)
	entries, e := os.ReadDir(dir)
	if want == "" || e != nil {
		return ""
	}
	best, most := "", -1
	for _, entry := range entries {
		if !entry.IsDir() || key(entry.Name()) != want {
			continue
		}
		children, _ := os.ReadDir(filepath.Join(dir, entry.Name()))
		if len(children) > most {
			best, most = entry.Name(), len(children)
		}
	}
	return best
}

func importFolder(root string, parts ...string) (string, error) {
	if e := os.MkdirAll(root, 0777); e != nil {
		return "", e
	}
	current, e := filepath.EvalSymlinks(root)
	if e != nil {
		return "", e
	}
	for _, part := range parts {
		next := filepath.Join(current, part)
		if _, e := os.Lstat(next); os.IsNotExist(e) {
			if existing := matchingFolder(current, part); existing != "" {
				next = filepath.Join(current, existing)
			}
		}
		current = next
		if e = os.Mkdir(current, 0777); e == nil {
			if e = ownPlacement(current, 0777); e != nil {
				return "", e
			}
		} else if !os.IsExist(e) {
			return "", e
		}
		info, e := os.Lstat(current)
		if e != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return "", errors.New("unsafe destination directory")
		}
	}
	return current, nil
}
func protectedPlacement(hooks *jobs.ImportHooks, path string) bool {
	if hooks == nil {
		return false
	}
	if hooks.ProtectedFile != nil {
		return hooks.ProtectedFile(path)
	}
	return hooks.Protected[path]
}
func LoadRoutes(path string) ([]Route, error) {
	data, e := os.ReadFile(path)
	if e != nil {
		return nil, e
	}
	return ParseRoutes(data)
}

// ParseRoutes validates operator routing JSON, from a file or ARRDIO_ROUTES.
func ParseRoutes(data []byte) ([]Route, error) {
	var routes []Route
	e := json.Unmarshal(data, &routes)
	if e != nil {
		return nil, e
	}
	for _, r := range routes {
		if !filepath.IsAbs(r.Path) || r.LibraryID == "" || r.Category == "" {
			return nil, errors.New("invalid operator route")
		}
	}
	return routes, nil
}

// Refresh uses supported scan APIs, serially. Acknowledgement is not completion;
// callers must still read presence after the service has indexed the item.
func (l *Libraries) Refresh(ctx context.Context, format, category string) error {
	found := false
	for _, route := range l.Routes {
		if route.Format != format || (category != "" && route.Category != category) {
			continue
		}
		found = true
		if _, e := l.index(ctx, route); e != nil {
			return e
		}
		l.mu.Lock()
		endpoint, token := "", ""
		switch route.Service {
		case "kavita":
			endpoint = l.KavitaURL + "/api/Library/scan?libraryId=" + escape(route.LibraryID)
			token = l.KavitaToken
		case "audiobookshelf":
			endpoint = l.ABSURL + "/api/libraries/" + escape(route.LibraryID) + "/scan"
			token = l.ABSToken
		}
		req, e := http.NewRequestWithContext(ctx, "POST", endpoint, nil)
		if e != nil {
			l.mu.Unlock()
			return e
		}
		req.Header.Set("Authorization", "Bearer "+token)
		resp, e := Client.Do(req)
		if e != nil {
			l.mu.Unlock()
			return errors.New("library refresh unavailable")
		}
		resp.Body.Close()
		l.invalidateLibrary(route)
		l.mu.Unlock()
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return HTTPStatusError{resp.StatusCode}
		}
	}
	if !found {
		return errors.New("no compatible route")
	}
	return nil
}
func libraryCacheKey(route Route) string {
	return route.Service + ":" + route.LibraryID + ":" + route.Format
}

// Caller holds mu. A scan can change every representation view of a library.
func (l *Libraries) invalidateLibrary(route Route) {
	prefix := route.Service + ":" + route.LibraryID + ":"
	for key := range l.cache {
		if strings.HasPrefix(key, prefix) {
			delete(l.cache, key)
		}
	}
}

var _ = http.MethodGet
