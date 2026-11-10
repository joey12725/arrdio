package providers

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// A conversion killed mid-encode left its partial M4B in the job's staging
// folder; retries piled up copies until the disk filled.
func TestNormalizeM4BRemovesStaleAttempts(t *testing.T) {
	if _, e := exec.LookPath("ffmpeg"); e != nil {
		t.Skip("ffmpeg not installed")
	}
	dir := t.TempDir()
	track := filepath.Join(dir, "01.mp3")
	if out, e := exec.Command("ffmpeg", "-nostdin", "-v", "error", "-f", "lavfi", "-i", "sine=frequency=440:duration=2", "-c:a", "libmp3lame", track).CombinedOutput(); e != nil {
		t.Skip("ffmpeg cannot encode mp3: ", string(out))
	}
	stale := filepath.Join(dir, ".arrdio-convert-123")
	os.Mkdir(stale, 0700)
	os.WriteFile(filepath.Join(stale, "book.m4b"), make([]byte, 1024), 0600)
	out, e := NormalizeM4B(context.Background(), []string{track}, filepath.Join(dir, "book.m4b"), "Title", "Author")
	if e != nil {
		t.Fatal(e)
	}
	if _, e := os.Stat(stale); !os.IsNotExist(e) {
		t.Fatal("stale conversion folder kept")
	}
	if left, _ := filepath.Glob(filepath.Join(dir, ".arrdio-convert-*")); len(left) != 0 {
		t.Fatal("conversion folders left behind", left)
	}
	if _, e := os.Stat(out); e != nil {
		t.Fatal("output missing", e)
	}
}
