package jobs

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	_ "golang.org/x/image/webp"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"path"
	"sort"
	"strings"
	"time"
)

type PhotoInput struct {
	Data        string      `json:"data"`
	MIME        string      `json:"mime"`
	Name        string      `json:"name,omitempty"`
	Kind        string      `json:"kind,omitempty"`
	Formats     []string    `json:"formats,omitempty"`
	Constraints Constraints `json:"constraints,omitempty"`
}
type PhotoStatus struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	State string `json:"state"`
	Error string `json:"error,omitempty"`
}

var ErrPhotoCapacity = errors.New("pending photo capacity reached")

func (r *Runtime) SavePhoto(ctx context.Context, owner string, in PhotoInput) (string, error) {
	validated := Input{Title: "photo-validation", Constraints: in.Constraints}
	if e := validate(&validated); e != nil {
		return "", e
	}
	in.Constraints = validated.Constraints
	if owner == "" || len(in.Data) > 14<<20 || (in.MIME != "image/png" && in.MIME != "image/jpeg" && in.MIME != "image/webp") {
		return "", ErrInvalid
	}
	if in.Kind != "" && in.Kind != "book" && in.Kind != "manga" {
		return "", ErrInvalid
	}
	if len(in.Formats) > 3 {
		return "", ErrInvalid
	}
	in.Formats = append([]string(nil), in.Formats...)
	sort.Strings(in.Formats)
	for _, f := range in.Formats {
		if f != "ebook" && f != "audio" && f != "manga" {
			return "", ErrInvalid
		}
	}
	data, e := base64.StdEncoding.DecodeString(in.Data)
	if e != nil || len(data) == 0 || len(data) > 10<<20 {
		return "", ErrInvalid
	}
	config, detected, e := image.DecodeConfig(bytes.NewReader(data))
	if e != nil || config.Width > 8192 || config.Height > 8192 || config.Width*config.Height > 24_000_000 || "image/"+detected != in.MIME {
		return "", ErrInvalid
	}
	identity := owner + string(data)
	identity += in.Constraints.scope()
	if in.Kind != "" || len(in.Formats) > 0 {
		options, _ := json.Marshal(struct {
			Kind    string
			Formats []string
		}{in.Kind, in.Formats})
		identity += "\x00" + string(options)
	}
	id := tokenHash(identity)
	name := path.Base(strings.ReplaceAll(in.Name, "\\", "/"))
	if name == "." || name == "/" || name == "" {
		name = "Photo " + id[:8]
	}
	runes := []rune(name)
	if len(runes) > 200 {
		name = string(runes[:200])
	}
	tx, e := r.Store.db.BeginTx(ctx, nil)
	if e != nil {
		return "", e
	}
	defer tx.Rollback()
	var state string
	e = tx.QueryRowContext(ctx, `SELECT state FROM photo_units WHERE hash=?`, id).Scan(&state)
	if e != nil && !errors.Is(e, sql.ErrNoRows) {
		return "", e
	}
	if state == "" || state == "failed" {
		var pending int
		if e = tx.QueryRowContext(ctx, `SELECT count(*) FROM photo_units WHERE owner=? AND state IN ('queued','extracting')`, owner).Scan(&pending); e != nil {
			return "", e
		}
		if pending >= 100 {
			return "", ErrPhotoCapacity
		}
	}
	if _, e = tx.ExecContext(ctx, `DELETE FROM photo_retries WHERE hash=? AND EXISTS(SELECT 1 FROM photo_units WHERE hash=? AND state='failed')`, id, id); e != nil {
		return "", e
	}
	if _, e = tx.ExecContext(ctx, `INSERT INTO photo_units(hash,owner,data,mime,created) VALUES(?,?,?,?,?) ON CONFLICT(hash) DO UPDATE SET state='queued',data=excluded.data,result=NULL,error='',created=excluded.created WHERE photo_units.state='failed'`, id, owner, data, in.MIME, time.Now().Unix()); e != nil {
		return "", e
	}
	formats, _ := json.Marshal(in.Formats)
	if _, e = tx.ExecContext(ctx, `INSERT OR IGNORE INTO photo_options(hash,name,kind,formats) VALUES(?,?,?,?)`, id, name, in.Kind, string(formats)); e != nil {
		return "", e
	}
	constraints, _ := json.Marshal(in.Constraints)
	if _, e = tx.ExecContext(ctx, `INSERT OR IGNORE INTO photo_constraints VALUES(?,?)`, id, constraints); e != nil {
		return "", e
	}
	return id, tx.Commit()
}
func (r *Runtime) Photos(ctx context.Context, owner string) ([]PhotoStatus, error) {
	rows, e := r.Store.db.QueryContext(ctx, `SELECT p.hash,COALESCE(o.name,'Photo upload'),p.state,p.error FROM photo_units p LEFT JOIN photo_options o ON o.hash=p.hash WHERE p.owner=? ORDER BY p.created DESC LIMIT 100`, owner)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []PhotoStatus{}
	for rows.Next() {
		var p PhotoStatus
		if e = rows.Scan(&p.ID, &p.Name, &p.State, &p.Error); e != nil {
			return nil, e
		}
		out = append(out, p)
	}
	return out, rows.Err()
}
