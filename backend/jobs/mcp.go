package jobs

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"net/http"
)

//go:embed integration/*
var integrationFiles embed.FS

func (r *Runtime) MCP() http.Handler {
	s := mcp.NewServer(&mcp.Implementation{Name: "arrdio", Version: "2.0.0"}, nil)
	type EnqueueArgs struct {
		Key   string  `json:"key" jsonschema:"Idempotency key for this batch"`
		Items []Input `json:"items"`
	}
	mcp.AddTool(s, &mcp.Tool{Name: "enqueue", Description: "Persist book requests and automatically acquire desired representations. Defaults to ebook and audio; manga defaults to manga."}, func(ctx context.Context, _ *mcp.CallToolRequest, a EnqueueArgs) (*mcp.CallToolResult, any, error) {
		if !scopeAllowed(ctx, "acquire") {
			return nil, nil, errors.New("insufficient scope")
		}
		owner, _ := ctx.Value(principalKey{}).(string)
		v, e := r.Store.Enqueue(ctx, owner, a.Key, a.Items)
		return nil, v, e
	})
	type StatusArgs struct{}
	mcp.AddTool(s, &mcp.Tool{Name: "status", Description: "Aggregate durable representation counts, including blocked work."}, func(ctx context.Context, _ *mcp.CallToolRequest, _ StatusArgs) (*mcp.CallToolResult, any, error) {
		if !scopeAllowed(ctx, "status") {
			return nil, nil, errors.New("insufficient scope")
		}
		v, e := r.Store.Counts(ctx)
		return nil, v, e
	})
	type ListArgs struct {
		Limit  int    `json:"limit"`
		Offset int    `json:"offset"`
		Sort   string `json:"sort"`
	}
	mcp.AddTool(s, &mcp.Tool{Name: "requests", Description: "Paged requests; sort by queue, title, -title, author, -author."}, func(ctx context.Context, _ *mcp.CallToolRequest, a ListArgs) (*mcp.CallToolResult, any, error) {
		if !scopeAllowed(ctx, "status") {
			return nil, nil, errors.New("insufficient scope")
		}
		if a.Limit == 0 {
			a.Limit = 50
		}
		if a.Sort == "" {
			a.Sort = "queue"
		}
		v, e := r.Store.List(ctx, a.Limit, a.Offset, a.Sort)
		return nil, v, e
	})
	type ControlArgs struct {
		ID     int64  `json:"id"`
		Format string `json:"format,omitempty"`
		Action string `json:"action"`
	}
	mcp.AddTool(s, &mcp.Tool{Name: "control", Description: "Cancel, retry missing work, or bump a request. Cancel never deletes imported content."}, func(ctx context.Context, _ *mcp.CallToolRequest, a ControlArgs) (*mcp.CallToolResult, any, error) {
		if !scopeAllowed(ctx, "acquire") {
			return nil, nil, errors.New("insufficient scope")
		}
		e := r.Store.Control(ctx, a.ID, a.Format, a.Action)
		return nil, map[string]bool{"ok": e == nil}, e
	})
	mcp.AddTool(s, &mcp.Tool{Name: "quota", Description: "Aggregate household ebook allowance; no account secrets."}, func(ctx context.Context, _ *mcp.CallToolRequest, _ StatusArgs) (*mcp.CallToolResult, any, error) {
		if !scopeAllowed(ctx, "status") {
			return nil, nil, errors.New("insufficient scope")
		}
		v, e := r.Quota(ctx)
		return nil, v, e
	})
	type SearchArgs struct {
		Query string `json:"query"`
	}
	mcp.AddTool(s, &mcp.Tool{Name: "import_photo", Description: "Persist one image for automatic Luna extraction and enqueue. Base64 PNG/JPEG/WebP up to 10 MB. Optional formats/kind override the normal book defaults; repeated identical images/options are idempotent."}, func(ctx context.Context, _ *mcp.CallToolRequest, a PhotoInput) (*mcp.CallToolResult, any, error) {
		if !scopeAllowed(ctx, "acquire") {
			return nil, nil, errors.New("insufficient scope")
		}
		owner, _ := ctx.Value(principalKey{}).(string)
		id, e := r.SavePhoto(ctx, owner, a)
		return nil, map[string]string{"id": id}, e
	})
	mcp.AddTool(s, &mcp.Tool{Name: "photos", Description: "Read this pairing owner's extraction units and operational errors."}, func(ctx context.Context, _ *mcp.CallToolRequest, _ StatusArgs) (*mcp.CallToolResult, any, error) {
		if !scopeAllowed(ctx, "status") {
			return nil, nil, errors.New("insufficient scope")
		}
		owner, _ := ctx.Value(principalKey{}).(string)
		out, e := r.Photos(ctx, owner)
		return nil, out, e
	})
	mcp.AddTool(s, &mcp.Tool{Name: "catalog", Description: "Optional metadata search; ordinary enqueue does not require it."}, func(ctx context.Context, _ *mcp.CallToolRequest, a SearchArgs) (*mcp.CallToolResult, any, error) {
		if !scopeAllowed(ctx, "status") {
			return nil, nil, errors.New("insufficient scope")
		}
		if r.SearchCatalog == nil {
			return nil, []Candidate{}, nil
		}
		v, e := r.SearchCatalog(ctx, a.Query)
		return nil, v, e
	})
	mcp.AddTool(s, &mcp.Tool{Name: "identity", Description: "Verify paired identity without acquiring anything."}, func(ctx context.Context, _ *mcp.CallToolRequest, _ StatusArgs) (*mcp.CallToolResult, any, error) {
		if !scopeAllowed(ctx, "status") {
			return nil, nil, errors.New("insufficient scope")
		}
		return nil, map[string]any{"owner": ctx.Value(principalKey{}), "resource": r.PublicURL}, nil
	})
	type CandidatesArgs struct {
		ID     int64  `json:"id"`
		Format string `json:"format"`
	}
	mcp.AddTool(s, &mcp.Tool{Name: "candidates", Description: "Optional alternative source results for correcting a match."}, func(ctx context.Context, _ *mcp.CallToolRequest, a CandidatesArgs) (*mcp.CallToolResult, any, error) {
		if !scopeAllowed(ctx, "status") {
			return nil, nil, errors.New("insufficient scope")
		}
		v, e := r.Candidates(ctx, a.ID, a.Format)
		return nil, v, e
	})
	type CorrectArgs struct {
		ID        int64  `json:"id"`
		Format    string `json:"format"`
		Candidate string `json:"candidate"`
	}
	mcp.AddTool(s, &mcp.Tool{Name: "refresh", Description: "Request supported library refresh for a representation; subsequent status determines actual availability."}, func(ctx context.Context, _ *mcp.CallToolRequest, a CandidatesArgs) (*mcp.CallToolResult, any, error) {
		if !scopeAllowed(ctx, "categorize") {
			return nil, nil, errors.New("insufficient scope")
		}
		e := r.Refresh(ctx, a.ID, a.Format)
		return nil, map[string]bool{"requested": e == nil}, e
	})
	mcp.AddTool(s, &mcp.Tool{Name: "correct", Description: "Choose a previously listed candidate; active ambiguous transfers cannot be replaced."}, func(ctx context.Context, _ *mcp.CallToolRequest, a CorrectArgs) (*mcp.CallToolResult, any, error) {
		if !scopeAllowed(ctx, "acquire") {
			return nil, nil, errors.New("insufficient scope")
		}
		e := r.Correct(ctx, a.ID, a.Format, a.Candidate)
		return nil, map[string]bool{"ok": e == nil}, e
	})
	type CategoryArgs struct {
		IDs      []int64 `json:"ids"`
		Category string  `json:"category"`
	}
	mcp.AddTool(s, &mcp.Tool{Name: "categorize", Description: "Assign an allowed category to compatible representations and queue placement reconciliation."}, func(ctx context.Context, _ *mcp.CallToolRequest, a CategoryArgs) (*mcp.CallToolResult, any, error) {
		if !scopeAllowed(ctx, "categorize") {
			return nil, nil, errors.New("insufficient scope")
		}
		e := r.Categorize(ctx, a.IDs, a.Category)
		return nil, map[string]bool{"ok": e == nil}, e
	})
	mcp.AddTool(s, &mcp.Tool{Name: "routing", Description: "Inspect allowed categories and destination library identities, without arbitrary filesystem paths."}, func(ctx context.Context, _ *mcp.CallToolRequest, _ StatusArgs) (*mcp.CallToolResult, any, error) {
		if !scopeAllowed(ctx, "status") {
			return nil, nil, errors.New("insufficient scope")
		}
		rows, e := r.Store.db.QueryContext(ctx, `SELECT category,format,library,library_id FROM routing ORDER BY category`)
		if e != nil {
			return nil, nil, e
		}
		defer rows.Close()
		out := []map[string]string{}
		for rows.Next() {
			var c, f, l, id string
			if e = rows.Scan(&c, &f, &l, &id); e != nil {
				return nil, nil, e
			}
			out = append(out, map[string]string{"category": c, "format": f, "library": l, "library_id": id})
		}
		return nil, out, rows.Err()
	})
	_ = json.Valid
	return r.dualEraMCP(mcp.NewStreamableHTTPHandler(func(_ *http.Request) *mcp.Server { return s }, &mcp.StreamableHTTPOptions{Stateless: true, JSONResponse: true}))
}
