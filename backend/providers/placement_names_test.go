package providers

import (
	"archive/zip"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/ashkaty/arrdio-api/jobs"
)

// Kavita names a PDF or comic series from its file name, and readback matches
// that name against the request, so "book.pdf" never became available.
func TestImportNamesFilesTheWayLibrariesReadThem(t *testing.T) {
	src := t.TempDir()
	write := func(name string) string {
		path := filepath.Join(src, name)
		switch filepath.Ext(name) {
		case ".cbz":
			f, _ := os.Create(path)
			z := zip.NewWriter(f)
			w, _ := z.Create("001.jpg")
			w.Write([]byte("page"))
			z.Close()
			f.Close()
		case ".m4b":
			if e := exec.Command("ffmpeg", "-nostdin", "-v", "error", "-f", "lavfi", "-i", "sine=duration=0.2", "-c:a", "aac", path).Run(); e != nil {
				t.Skip("ffmpeg unavailable")
			}
		default:
			os.WriteFile(path, append(append([]byte("%PDF-1.4\n"), make([]byte, 2048)...), []byte("\n%%EOF\n")...), 0600)
		}
		return path
	}
	cases := []struct {
		in     jobs.Input
		format string
		file   string
		want   string
	}{
		{jobs.Input{Title: "Middlemarch", Author: "George Eliot"}, "ebook", "book.pdf", filepath.Join("George Eliot", "Middlemarch", "Middlemarch (George Eliot).pdf")},
		{jobs.Input{Title: "Green Hour", Author: "Riku Nakamura", Volume: "1", Kind: "manga"}, "manga", "Green Hour v01 (2020) (Digital) (scan0).cbz", filepath.Join("Green Hour", "Green Hour v01.cbz")},
		{jobs.Input{Title: "The Age of Innocence", Author: "Edith Wharton"}, "audio", "normalized.m4b", filepath.Join("Edith Wharton", "The Age of Innocence", "The Age of Innocence.m4b")},
	}
	for _, c := range cases {
		root := t.TempDir()
		library := &Libraries{Routes: []Route{{Category: c.format, Format: c.format, Service: "audiobookshelf", LibraryID: "1", Path: root}}}
		_, importErr := library.Import(context.Background(), c.in, c.format, []string{write(c.file)}, "")
		if _, e := os.Stat(filepath.Join(root, c.want)); e != nil {
			t.Log(importErr)
			found, _ := filepath.Glob(filepath.Join(root, "*", "*"))
			deeper, _ := filepath.Glob(filepath.Join(root, "*", "*", "*"))
			t.Errorf("%s: want %s, placed %v %v", c.in.Title, c.want, found, deeper)
		}
	}
	if got := libraryName(jobs.Input{Title: "A"}, "audio", "Track 3", 2, 12); got != "0003 Track 3" {
		t.Errorf("multi-track name %q", got)
	}
}

func TestImportRemovesStalePartialCopies(t *testing.T) {
	root, src := t.TempDir(), t.TempDir()
	folder := filepath.Join(root, "George Eliot", "Middlemarch")
	os.MkdirAll(folder, 0777)
	stale := filepath.Join(folder, ".arrdio-import-123")
	os.WriteFile(stale, make([]byte, 64), 0600)
	pdf := filepath.Join(src, "book.pdf")
	os.WriteFile(pdf, append(append([]byte("%PDF-1.4\n"), make([]byte, 2048)...), []byte("\n%%EOF\n")...), 0600)
	library := &Libraries{Routes: []Route{{Category: "fiction", Format: "ebook", Service: "kavita", LibraryID: "1", Path: root}}}
	library.Import(context.Background(), jobs.Input{Title: "Middlemarch", Author: "George Eliot"}, "ebook", []string{pdf}, "")
	if _, e := os.Stat(stale); !os.IsNotExist(e) {
		t.Fatal("stale partial copy left in the library folder")
	}
	if _, e := os.Stat(filepath.Join(folder, "Middlemarch (George Eliot).pdf")); e != nil {
		t.Fatal(e)
	}
}

func TestSafePartKeepsLibraryPunctuation(t *testing.T) {
	for in, want := range map[string]string{
		"Elowen: Past Winter’s End":        "Elowen Past Winter's End",
		"L. M. Montgomery":                          "L. M. Montgomery",
		"Liam O'Flaherty":                         "Liam O'Flaherty",
		"Faster, and Faster, and Faster": "Faster, and Faster, and Faster",
		"../etc/passwd":                        "etc passwd",
		"..":                                   "Unknown",
		"a/b\\c:d*e?f\"g<h>i|j":                "a b c d e f g h i j",
		".hidden.":                             "hidden",
		"Cien años de soledad":                 "Cien años de soledad",
	} {
		if got := safePart(in); got != want {
			t.Errorf("safePart(%q) = %q, want %q", in, got, want)
		}
	}
}
