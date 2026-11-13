package providers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/joey12725/arrdio/backend/jobs"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

type Soulseek struct{ URL, Key, DownloadRoot string }
type peerFile struct {
	Filename string `json:"filename"`
	Size     int64  `json:"size"`
	ID       string `json:"id,omitempty"`
	State    string `json:"state,omitempty"`
}
type peerSet struct {
	Username string                       `json:"username"`
	Files    []peerFile                   `json:"files"`
	Folder   string                       `json:"folder,omitempty"`
	Reused   map[string]jobs.RetainedFile `json:"reused,omitempty"`
}

func (s *Soulseek) Name() string { return "soulseek" }
func (s *Soulseek) headers() map[string]string {
	return map[string]string{"X-API-Key": s.Key, "Content-Type": "application/json"}
}
func (s *Soulseek) Search(ctx context.Context, in jobs.Input, format string) ([]jobs.Candidate, error) {
	if format != "audio" || s.URL == "" || s.Key == "" {
		return nil, nil
	}
	body, _ := json.Marshal(map[string]any{"searchText": in.Title + " " + in.Author, "searchTimeout": 10000, "maximumPeerQueueLength": 10, "minimumPeerUploadSpeed": 0})
	var search struct {
		ID string `json:"id"`
	}
	if e := jsonCall(ctx, Client, "POST", s.URL+"/api/v0/searches", bytes.NewReader(body), s.headers(), &search); e != nil {
		return nil, e
	}
	if search.ID == "" {
		return nil, errors.New("slskd search receipt absent")
	}
	// slskd returns no responses until a search completes, so wait for its
	// state rather than a fixed delay (a fixed 10 s read returned nothing live).
	// A search still running at the deadline is stopped, which completes it and
	// releases the responses gathered so far.
	stopped := false
	for deadline := time.Now().Add(slskdSearchWindow); ; {
		timer := time.NewTimer(slskdPoll)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, ctx.Err()
		case <-timer.C:
		}
		var status struct {
			State string `json:"state"`
		}
		if e := jsonCall(ctx, Client, "GET", s.URL+"/api/v0/searches/"+escape(search.ID), nil, s.headers(), &status); e != nil {
			return nil, e
		}
		if strings.Contains(status.State, "Completed") {
			break
		}
		if time.Now().After(deadline) {
			if stopped {
				break
			}
			stopped = true
			req, _ := http.NewRequestWithContext(ctx, "PUT", s.URL+"/api/v0/searches/"+escape(search.ID), nil)
			for k, v := range s.headers() {
				req.Header.Set(k, v)
			}
			if resp, e := Client.Do(req); e == nil {
				resp.Body.Close()
			}
		}
	}
	var responses []struct {
		Username          string     `json:"username"`
		HasFreeUploadSlot bool       `json:"hasFreeUploadSlot"`
		QueueLength       int        `json:"queueLength"`
		Files             []peerFile `json:"files"`
	}
	if e := jsonCall(ctx, Client, "GET", s.URL+"/api/v0/searches/"+escape(search.ID)+"/responses", nil, s.headers(), &responses); e != nil {
		return nil, e
	}
	sort.SliceStable(responses, func(i, j int) bool {
		if responses[i].HasFreeUploadSlot != responses[j].HasFreeUploadSlot {
			return responses[i].HasFreeUploadSlot
		}
		return responses[i].QueueLength < responses[j].QueueLength
	})
	out := []jobs.Candidate{}
	inspected := 0
	for _, peer := range responses {
		folders := map[string][]peerFile{}
		for _, f := range peer.Files {
			name := strings.ReplaceAll(f.Filename, "\\", "/")
			ext := strings.ToLower(path.Ext(name))
			if f.Size > 0 && (ext == ".mp3" || ext == ".m4b" || ext == ".flac") {
				folder := path.Dir(name)
				folders[folder] = append(folders[folder], f)
			}
		}
		for folder, files := range folders {
			if overlap(in.Title, folder) < 0.6 {
				continue
			}
			if !shortTitleMatches(in, jobs.Candidate{Title: path.Base(folder)}) {
				continue
			}
			if inspected >= 8 {
				return Rank(in, format, out), nil
			}
			inspected++
			// Search hits are not a directory inventory. Ask the peer for the whole
			// selected folder and include every supported audio file in that set.
			full, e := s.directory(ctx, peer.Username, folder)
			if e != nil || !completeSet(full) {
				continue
			}
			files = full
			sort.Slice(files, func(i, j int) bool { return naturalKey(files[i].Filename) < naturalKey(files[j].Filename) })
			extra, _ := json.Marshal(peerSet{Username: peer.Username, Files: files, Folder: folder})
			out = append(out, jobs.Candidate{ID: "slskd:" + peer.Username + ":" + folder, Provider: s.Name(), Title: path.Base(folder), Format: "audio", Extra: extra})
		}
	}
	return Rank(in, format, out), nil
}
func (s *Soulseek) directory(ctx context.Context, username, folder string) ([]peerFile, error) {
	body, _ := json.Marshal(map[string]string{"directory": strings.ReplaceAll(folder, "/", "\\")})
	var dirs []struct {
		Name  string     `json:"name"`
		Files []peerFile `json:"files"`
	}
	if e := jsonCall(ctx, Client, "POST", s.URL+"/api/v0/users/"+url.PathEscape(username)+"/directory", bytes.NewReader(body), s.headers(), &dirs); e != nil {
		return nil, e
	}
	out := []peerFile{}
	for _, d := range dirs {
		if strings.TrimRight(strings.ReplaceAll(d.Name, "\\", "/"), "/") != folder {
			continue
		}
		for _, f := range d.Files {
			if f.Size <= 0 || !audioFile(f.Filename) {
				continue
			}
			if !strings.ContainsAny(f.Filename, "/\\") {
				f.Filename = strings.ReplaceAll(folder, "/", "\\") + "\\" + f.Filename
			}
			out = append(out, f)
		}
	}
	return out, nil
}

// slskdPoll is the interval between search status checks; slskdSearchWindow is
// how long a search may run before it is stopped and its responses are read.
var (
	slskdPoll         = 2 * time.Second
	slskdSearchWindow = 15 * time.Second
)

var digitGroup = regexp.MustCompile(`\d+`)

// completeSet accepts a single M4B, or tracks whose numbers run exactly 1..N.
// The track number may sit anywhere in the name ("01 - Chapter 1", "Dracula 10",
// "Bram Stoker - Dracula 03-18"): it is the digit group, counted from the start
// or the end of the name, whose values across the folder are 1..N. Constant
// groups such as years or bitrates never form that run; sets whose numbering
// restarts per disc are rejected rather than guessed.
func completeSet(files []peerFile) bool {
	if len(files) == 1 {
		return strings.EqualFold(filepath.Ext(files[0].Filename), ".m4b")
	}
	if len(files) < 2 {
		return false
	}
	groups := make([][]int, len(files))
	shortest := -1
	for i, f := range files {
		name := path.Base(strings.ReplaceAll(f.Filename, "\\", "/"))
		name = strings.TrimSuffix(name, path.Ext(name))
		for _, g := range digitGroup.FindAllString(name, -1) {
			n, _ := strconv.Atoi(g)
			groups[i] = append(groups[i], n)
		}
		if shortest < 0 || len(groups[i]) < shortest {
			shortest = len(groups[i])
		}
	}
	for k := 0; k < shortest; k++ {
		for _, fromEnd := range []bool{false, true} {
			seen := map[int]bool{}
			for _, g := range groups {
				n := g[k]
				if fromEnd {
					n = g[len(g)-1-k]
				}
				if n < 1 || n > len(files) || seen[n] {
					break
				}
				seen[n] = true
			}
			if len(seen) == len(files) {
				return true
			}
		}
	}
	return false
}
func (s *Soulseek) Acquire(ctx context.Context, c jobs.Candidate, destination string) (jobs.Receipt, error) {
	var set peerSet
	if json.Unmarshal(c.Extra, &set) != nil || set.Username == "" || len(set.Files) == 0 {
		return jobs.Receipt{}, jobs.ErrInvalid
	}
	retained := map[string]jobs.RetainedFile{}
	for _, file := range jobs.RetainedFiles(ctx) {
		retained[file.Identity] = file
	}
	set.Reused = map[string]jobs.RetainedFile{}
	pending := []peerFile{}
	for _, file := range set.Files {
		identity := peerIdentity(set, file)
		cached, ok := retained[identity]
		if ok && cached.Size == file.Size {
			if info, e := os.Stat(cached.Path); e == nil && info.Size() == file.Size && ValidateMedia(cached.Path) == nil {
				set.Reused[file.Filename] = cached
				continue
			}
		}
		pending = append(pending, file)
	}
	body, _ := json.Marshal(pending)
	// The full selection is the durable receipt; API transfer state is reconciled by peer/path.
	receiptData, _ := json.Marshal(set)
	r := jobs.Receipt{ID: string(receiptData), State: "provider_wait", Ordered: true}
	if !jobs.HasTransferCheckpoint(ctx) {
		return r, jobs.SafeFailure{Err: errors.New("durable peer checkpoint required")}
	}
	if e := jobs.CheckpointTransfer(ctx, r); e != nil {
		return r, jobs.SafeFailure{Err: e}
	}
	if len(pending) == 0 {
		for _, file := range set.Files {
			r.Paths = append(r.Paths, set.Reused[file.Filename].Path)
		}
		r.State = "verified"
		return r, nil
	}
	var result any
	e := jsonCall(ctx, Client, "POST", s.URL+"/api/v0/transfers/downloads/"+url.PathEscape(set.Username), bytes.NewReader(body), s.headers(), &result)
	var status HTTPStatusError
	if errors.As(e, &status) && (status.Code == 400 || status.Code == 401 || status.Code == 403 || status.Code == 404) {
		return r, jobs.SafeFailure{Err: e}
	}
	return r, e
}
func (s *Soulseek) Prepare(ctx context.Context, c jobs.Candidate, destination string) (jobs.Receipt, error) {
	if !filepath.IsAbs(s.DownloadRoot) {
		return jobs.Receipt{}, errors.New("peer payload mount is not configured")
	}
	var set peerSet
	if json.Unmarshal(c.Extra, &set) != nil || set.Username == "" || len(set.Files) == 0 {
		return jobs.Receipt{}, jobs.ErrInvalid
	}
	data, _ := json.Marshal(set)
	return jobs.Receipt{ID: string(data), State: "provider_wait", Ordered: true}, nil
}
func (s *Soulseek) Reconcile(ctx context.Context, r jobs.Receipt) (jobs.Receipt, error) {
	var set peerSet
	if json.Unmarshal([]byte(r.ID), &set) != nil {
		return r, errors.New("slskd receipt incomplete")
	}
	var groups []struct {
		Username    string `json:"username"`
		Directories []struct {
			Files []peerFile `json:"files"`
		} `json:"directories"`
	}
	if e := jsonCall(ctx, Client, "GET", s.URL+"/api/v0/transfers/downloads", nil, s.headers(), &groups); e != nil {
		return r, e
	}
	states := map[string]string{}
	for _, g := range groups {
		if g.Username != set.Username {
			continue
		}
		for _, d := range g.Directories {
			for _, f := range d.Files {
				states[f.Filename] = strings.ToLower(f.State)
			}
		}
	}
	paths := []string{}
	completed := []jobs.RetainedFile{}
	failed, pending := 0, 0
	for _, file := range set.Files {
		if cached, ok := set.Reused[file.Filename]; ok {
			if info, e := os.Stat(cached.Path); e == nil && info.Size() == file.Size && ValidateMedia(cached.Path) == nil {
				paths = append(paths, cached.Path)
				completed = append(completed, cached)
				continue
			}
		}
		state := states[file.Filename]
		terminal := strings.Contains(state, "errored") || strings.Contains(state, "failed") || strings.Contains(state, "cancelled") || strings.Contains(state, "canceled") || strings.Contains(state, "rejected") || strings.Contains(state, "timedout")
		if terminal {
			failed++
			continue
		}
		if !strings.Contains(state, "succeeded") && state != "completed" {
			pending++
			continue
		}
		local := filepath.Join(s.DownloadRoot, filepath.FromSlash(peerLocalName(file.Filename)))
		rel, e := filepath.Rel(s.DownloadRoot, local)
		if e != nil || strings.HasPrefix(rel, "..") {
			return r, fmt.Errorf("unsafe peer filename")
		}
		if info, e := os.Stat(local); e != nil || info.Size() != file.Size {
			return r, fmt.Errorf("%w: peer payload missing or size mismatch", jobs.ErrPayloadPending)
		}
		if e = ValidateMedia(local); e != nil {
			return r, e
		}
		paths = append(paths, local)
		completed = append(completed, jobs.RetainedFile{Identity: peerIdentity(set, file), Path: local, Size: file.Size})
	}
	r.Completed = completed
	r.Ordered = true
	if failed > 0 && pending == 0 {
		return r, jobs.RetryTransferFailure{Err: errors.New("peer transfers terminated with errors")}
	}
	if failed > 0 || pending > 0 {
		r.State = "provider_wait"
		return r, nil
	}
	r.Paths = paths
	r.State = "verified"
	return r, nil
}

// peerLocalName is where slskd stores a completed download: the remote file's
// immediate parent directory and its name, not the peer's full share path.
func peerLocalName(remote string) string {
	parts := strings.Split(strings.ReplaceAll(remote, "\\", "/"), "/")
	if len(parts) < 2 {
		return parts[0]
	}
	return parts[len(parts)-2] + "/" + parts[len(parts)-1]
}
func peerIdentity(set peerSet, file peerFile) string {
	name := strings.ReplaceAll(file.Filename, "\\", "/")
	folder := strings.TrimSuffix(strings.ReplaceAll(set.Folder, "\\", "/"), "/")
	if folder != "" {
		name = strings.TrimPrefix(name, folder+"/")
	}
	return "slskd:" + strings.ToLower(name) + ":" + strconv.FormatInt(file.Size, 10)
}
