package providers

import (
	"archive/zip"
	"context"
	"encoding/xml"
	"errors"
	"github.com/joey12725/arrdio/backend/jobs"
	"io"
	"path/filepath"
	"strings"
)

type BookMetadata struct{ Title, Author, Language string }

// ReadEPUBMetadata follows the package's own container pointer rather than
// associating metadata by result index or by guessing an archive member name.
func ReadEPUBMetadata(path string) (BookMetadata, error) {
	var metadata BookMetadata
	archive, e := zip.OpenReader(path)
	if e != nil {
		return metadata, e
	}
	defer archive.Close()
	read := func(name string, out any) error {
		for _, file := range archive.File {
			if file.Name == name {
				if file.UncompressedSize64 > 2<<20 {
					return errors.New("metadata exceeds limit")
				}
				stream, e := file.Open()
				if e != nil {
					return e
				}
				defer stream.Close()
				return xml.NewDecoder(io.LimitReader(stream, 2<<20)).Decode(out)
			}
		}
		return errors.New("package metadata unavailable")
	}
	var container struct {
		Rootfiles []struct {
			Path string `xml:"full-path,attr"`
		} `xml:"rootfiles>rootfile"`
	}
	if e = read("META-INF/container.xml", &container); e != nil {
		return metadata, e
	}
	if len(container.Rootfiles) == 0 {
		return metadata, errors.New("missing package pointer")
	}
	name := container.Rootfiles[0].Path
	if strings.HasPrefix(name, "/") || strings.Contains(name, "..") {
		return metadata, errors.New("unsafe package pointer")
	}
	var pkg struct {
		Metadata struct {
			Titles    []string `xml:"title"`
			Creators  []string `xml:"creator"`
			Languages []string `xml:"language"`
		} `xml:"metadata"`
	}
	if e = read(name, &pkg); e != nil {
		return metadata, e
	}
	if len(pkg.Metadata.Titles) > 0 {
		metadata.Title = strings.TrimSpace(pkg.Metadata.Titles[0])
	}
	metadata.Author = strings.Join(pkg.Metadata.Creators, ", ")
	if len(pkg.Metadata.Languages) > 0 {
		metadata.Language = pkg.Metadata.Languages[0]
	}
	return metadata, nil
}
func VerifyBookIdentity(path string, candidate jobs.Candidate, requirements ...jobs.Constraints) error {
	if !strings.EqualFold(filepath.Ext(path), ".epub") {
		return nil
	}
	meta, e := ReadEPUBMetadata(path)
	if e != nil {
		return e
	}
	if meta.Title != "" && overlap(candidate.Title, meta.Title) < 0.5 && overlap(meta.Title, candidate.Title) < 0.5 {
		return errors.New("ebook title conflicts with selected result")
	}
	if candidate.Author != "" && meta.Author != "" && overlap(candidate.Author, meta.Author) < 0.4 {
		return errors.New("ebook author conflicts with selected result")
	}
	// An artifact that states no language ("", und, mul, zxx) neither confirms
	// nor contradicts the source label the selection already required.
	stated := !map[string]bool{"": true, "und": true, "mul": true, "zxx": true}[strings.ToLower(strings.TrimSpace(meta.Language))]
	if len(requirements) > 0 && requirements[0].Language != "" && stated {
		if !jobs.LanguageMatches(requirements[0].Language, meta.Language) {
			return errors.New("ebook language does not satisfy the explicit requirement")
		}
	}
	return nil
}
func VerifyArtifact(ctx context.Context, in jobs.Input, receipt jobs.Receipt) error {
	title, author := receipt.Title, receipt.Author
	if title == "" {
		title = in.Title
	}
	if author == "" {
		author = in.Author
	}
	for _, path := range receipt.Paths {
		if e := ctx.Err(); e != nil {
			return e
		}
		if e := VerifyBookIdentity(path, jobs.Candidate{Title: title, Author: author}, in.Constraints); e != nil {
			return e
		}
	}
	return nil
}
