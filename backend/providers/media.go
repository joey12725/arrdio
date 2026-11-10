package providers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/ashkaty/arrdio-api/jobs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

type Probe struct {
	Format struct {
		Duration string `json:"duration"`
	} `json:"format"`
	Streams []struct {
		CodecType string `json:"codec_type"`
		CodecName string `json:"codec_name"`
	} `json:"streams"`
}

func probe(ctx context.Context, path string) (Probe, error) {
	var p Probe
	out, e := exec.CommandContext(ctx, "ffprobe", "-v", "error", "-show_format", "-show_streams", "-of", "json", path).Output()
	if e != nil {
		return p, errors.New("media probe failed")
	}
	e = json.Unmarshal(out, &p)
	return p, e
}
func ValidateMedia(path string) error {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".mp3", ".m4b", ".m4a", ".aac", ".flac", ".ogg", ".opus":
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		p, e := probe(ctx, path)
		if e != nil {
			// A timeout is the disk's problem; a refusal is the file's
			// (xHE-AAC M4Bs from Audible: "Audio object type 42").
			if ctx.Err() != nil {
				return e
			}
			return jobs.UnusableCandidate{Err: errors.New("audio cannot be decoded")}
		}
		d, _ := strconv.ParseFloat(p.Format.Duration, 64)
		if d <= 0 {
			return jobs.UnusableCandidate{Err: errors.New("audio has no duration")}
		}
		for _, s := range p.Streams {
			if s.CodecType == "audio" {
				return nil
			}
		}
		return jobs.UnusableCandidate{Err: errors.New("no audio stream")}
	default:
		return Validate(path)
	}
}
func VerifyRetained(ctx context.Context, format string, paths []string) ([]string, error) {
	out := []string{}
	media := 0
	for _, path := range paths {
		if e := ctx.Err(); e != nil {
			return nil, e
		}
		ext := strings.ToLower(filepath.Ext(path))
		supported := false
		artwork := false
		if format == "audio" {
			supported = audioFile(path)
			artwork = ext == ".jpg" || ext == ".png"
		} else if format == "ebook" {
			supported = ext == ".epub" || ext == ".pdf"
		} else if format == "manga" {
			supported = ext == ".cbz" || ext == ".cbr" || ext == ".pdf" || ext == ".epub"
		}
		if !supported && !artwork {
			continue
		}
		if e := ValidateMedia(path); e != nil {
			return nil, e
		}
		out = append(out, path)
		if supported {
			media++
		}
	}
	if media == 0 {
		return nil, errors.New("no verified local media")
	}
	return out, nil
}
func naturalKey(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); {
		if s[i] >= '0' && s[i] <= '9' {
			j := i
			for j < len(s) && s[j] >= '0' && s[j] <= '9' {
				j++
			}
			n, _ := strconv.Atoi(s[i:j])
			fmt.Fprintf(&b, "%012d", n)
			i = j
		} else {
			b.WriteByte(s[i])
			i++
		}
	}
	return b.String()
}

// NormalizeM4B creates a library copy; source files are never modified. One caller
// at a time is enforced by the audio lane. AAC can be remuxed; MP3 is transcoded.
func NormalizeM4B(ctx context.Context, paths []string, destination, title, author string) (string, error) {
	tracks := []string{}
	cover := ""
	for _, p := range paths {
		if audioFile(p) {
			tracks = append(tracks, p)
		} else if cover == "" && (strings.EqualFold(filepath.Ext(p), ".jpg") || strings.EqualFold(filepath.Ext(p), ".png")) {
			cover = p
		}
	}
	if !jobs.OrderedTracks(ctx) {
		sort.Slice(tracks, func(i, j int) bool { return naturalKey(tracks[i]) < naturalKey(tracks[j]) })
	}
	if len(tracks) == 0 {
		return "", errors.New("empty audio set")
	}
	if len(tracks) == 1 && strings.EqualFold(filepath.Ext(tracks[0]), ".m4b") {
		return tracks[0], ValidateMedia(tracks[0])
	}
	total := 0.0
	copyCodec := true
	var list strings.Builder
	var meta strings.Builder
	meta.WriteString(";FFMETADATA1\n")
	for i, path := range tracks {
		if strings.ContainsAny(path, "'\n\r") {
			return "", errors.New("unsupported track path")
		}
		p, e := probe(ctx, path)
		if e != nil {
			return "", e
		}
		duration, e := strconv.ParseFloat(p.Format.Duration, 64)
		if e != nil || duration <= 0 {
			return "", errors.New("invalid duration")
		}
		audio := false
		for _, s := range p.Streams {
			if s.CodecType == "audio" {
				audio = true
				if s.CodecName != "aac" {
					copyCodec = false
				}
			}
		}
		if !audio {
			return "", errors.New("missing track audio")
		}
		fmt.Fprintf(&list, "file '%s'\n", path)
		fmt.Fprintf(&meta, "[CHAPTER]\nTIMEBASE=1/1000\nSTART=%d\nEND=%d\ntitle=Chapter %d\n", int64(total*1000), int64((total+duration)*1000), i+1)
		total += duration
	}
	// An attempt killed mid-encode (restart, crash) skips the deferred cleanup;
	// its partial output can be as large as the book. The job's lease makes
	// this the only conversion in its staging folder, so leftovers are stale.
	if stale, _ := filepath.Glob(filepath.Join(filepath.Dir(destination), ".arrdio-convert-*")); len(stale) > 0 {
		for _, old := range stale {
			os.RemoveAll(old)
		}
	}
	dir, e := os.MkdirTemp(filepath.Dir(destination), ".arrdio-convert-")
	if e != nil {
		return "", e
	}
	defer os.RemoveAll(dir)
	os.WriteFile(filepath.Join(dir, "tracks.txt"), []byte(list.String()), 0600)
	os.WriteFile(filepath.Join(dir, "chapters.txt"), []byte(meta.String()), 0600)
	tmp := filepath.Join(dir, "book.m4b")
	args := []string{"-nostdin", "-v", "error", "-f", "concat", "-safe", "0", "-i", filepath.Join(dir, "tracks.txt"), "-i", filepath.Join(dir, "chapters.txt")}
	if cover != "" {
		args = append(args, "-i", cover)
	}
	args = append(args, "-map", "0:a:0", "-map_metadata", "1", "-map_chapters", "1", "-metadata", "title="+title, "-metadata", "artist="+author, "-threads", "1")
	if cover != "" {
		args = append(args, "-map", "2:v:0", "-c:v", "mjpeg", "-disposition:v:0", "attached_pic")
	}
	if copyCodec {
		args = append(args, "-c:a", "copy")
	} else {
		bitrate := os.Getenv("ARRDIO_AUDIO_BITRATE")
		if bitrate == "" {
			bitrate = "96k"
		}
		switch bitrate {
		case "64k", "96k", "128k", "160k", "192k":
		default:
			return "", errors.New("unsupported conversion bitrate")
		}
		args = append(args, "-c:a", "aac", "-b:a", bitrate)
	}
	args = append(args, "-movflags", "+faststart", tmp)
	if exec.CommandContext(ctx, "ffmpeg", args...).Run() != nil {
		return "", errors.New("conversion failed; originals retained")
	}
	p, e := probe(ctx, tmp)
	if e != nil {
		return "", e
	}
	duration, _ := strconv.ParseFloat(p.Format.Duration, 64)
	// 1%, but at least a second: encoder padding on a short input exceeds 1%.
	slack := total * 0.01
	if slack < 1 {
		slack = 1
	}
	if duration < total-slack || duration > total+slack {
		return "", errors.New("conversion duration mismatch")
	}
	if e = os.Rename(tmp, destination); e != nil {
		return "", e
	}
	return destination, nil
}
