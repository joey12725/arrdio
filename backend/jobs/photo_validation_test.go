package jobs

import (
	"bytes"
	"context"
	"encoding/base64"
	"image"
	"image/png"
	"path/filepath"
	"strings"
	"testing"
)

func TestInvalidExtractedUnitDoesNotBlockNextPhoto(t *testing.T) {
	r := runtimeTest(t, filepath.Join(t.TempDir(), "jobs.db"))
	r.ExtractPhoto = func(_ context.Context, data []byte, _ string) ([]Input, error) {
		if string(data) == "bad" {
			return []Input{{Title: strings.Repeat("x", 1001)}}, nil
		}
		return []Input{{Title: "Valid next photo"}}, nil
	}
	r.Store.db.Exec(`INSERT INTO photo_units(hash,owner,data,mime,created) VALUES('bad','owner',?,'image/png',1),('good','owner',?,'image/png',2)`, []byte("bad"), []byte("good"))
	r.processPhoto(ctx)
	r.processPhoto(ctx)
	var failed, complete int
	r.Store.db.QueryRow(`SELECT count(*) FROM photo_units WHERE state='failed'`).Scan(&failed)
	r.Store.db.QueryRow(`SELECT count(*) FROM photo_units WHERE state='complete'`).Scan(&complete)
	if failed != 1 || complete != 1 {
		t.Fatal(failed, complete)
	}
}
func TestPhotoFormatOverrideAndRepeatedUpload(t *testing.T) {
	r := runtimeTest(t, filepath.Join(t.TempDir(), "jobs.db"))
	var buffer bytes.Buffer
	png.Encode(&buffer, image.NewRGBA(image.Rect(0, 0, 2, 2)))
	in := PhotoInput{Data: base64.StdEncoding.EncodeToString(buffer.Bytes()), MIME: "image/png", Name: "cover.png", Formats: []string{"ebook"}}
	calls := 0
	r.ExtractPhoto = func(context.Context, []byte, string) ([]Input, error) {
		calls++
		return []Input{{Title: "A", Kind: "book"}}, nil
	}
	first, e := r.SavePhoto(ctx, "owner", in)
	if e != nil {
		t.Fatal(e)
	}
	again, e := r.SavePhoto(ctx, "owner", in)
	if e != nil || again != first {
		t.Fatal(first, again, e)
	}
	r.processPhoto(ctx)
	books, e := r.Store.List(ctx, 10, 0, "queue")
	if e != nil || len(books) != 1 || len(books[0].Representations) != 1 || books[0].Representations[0].Format != "ebook" {
		t.Fatal(books, e)
	}
	in.Formats = nil
	second, e := r.SavePhoto(ctx, "owner", in)
	if e != nil || second == first {
		t.Fatal(second, e)
	}
	r.processPhoto(ctx)
	r.SavePhoto(ctx, "owner", in)
	r.processPhoto(ctx)
	books, e = r.Store.List(ctx, 10, 0, "queue")
	if e != nil || len(books) != 1 || len(books[0].Representations) != 2 || calls != 2 {
		t.Fatal(books, e, calls)
	}
	photos, e := r.Photos(ctx, "owner")
	if e != nil || len(photos) != 2 || photos[0].Name != "cover.png" {
		t.Fatal(photos, e)
	}
}
func TestPhotoHardRequirementsAreDurableAndScoped(t *testing.T) {
	path := filepath.Join(t.TempDir(), "jobs.db")
	r := runtimeTest(t, path)
	var buffer bytes.Buffer
	png.Encode(&buffer, image.NewRGBA(image.Rect(0, 0, 2, 2)))
	photo := PhotoInput{Data: base64.StdEncoding.EncodeToString(buffer.Bytes()), MIME: "image/png", Constraints: Constraints{Language: "English", Container: "epub"}}
	id, e := r.SavePhoto(ctx, "owner", photo)
	if e != nil {
		t.Fatal(e)
	}
	photo.Constraints = Constraints{}
	other, e := r.SavePhoto(ctx, "owner", photo)
	if e != nil || id == other {
		t.Fatal("constraint-specific upload collapsed", id, other, e)
	}
	r.Store.Close()
	r = runtimeTest(t, path)
	r.ExtractPhoto = func(context.Context, []byte, string) ([]Input, error) { return []Input{{Title: "A"}}, nil }
	r.processPhoto(ctx)
	r.processPhoto(ctx)
	list, e := r.Store.List(ctx, 10, 0, "queue")
	if e != nil || len(list) != 2 {
		t.Fatal(list, e)
	}
	strict := 0
	for _, book := range list {
		if book.Constraints.Language == "english" && book.Constraints.Container == "epub" {
			strict++
		}
	}
	if strict != 1 {
		t.Fatal("photo extraction discarded hard requirements", list)
	}
}
