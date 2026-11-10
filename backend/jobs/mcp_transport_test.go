package jobs

import (
	"bytes"
	"encoding/json"
	"net/http/httptest"
	"path/filepath"
	"testing"
)

func TestModernMCPDiscoveryHeadersAndToolScopes(t *testing.T) {
	r := runtimeTest(t, filepath.Join(t.TempDir(), "jobs.db"))
	token := "read-only-test-grant"
	r.Store.db.Exec(`INSERT INTO grants VALUES(?,?,?,?,0,0)`, tokenHash(token), "reader", "fixture", "status")
	invoke := func(method, name, version, headerVersion string, origin string) *httptest.ResponseRecorder {
		arguments := map[string]any{}
		if name == "enqueue" {
			arguments = map[string]any{"key": "scope-test", "items": []any{map[string]string{"title": "A", "author": ""}}}
		}
		body := map[string]any{"jsonrpc": "2.0", "id": 1, "method": method, "params": map[string]any{"_meta": map[string]any{"io.modelcontextprotocol/protocolVersion": version, "io.modelcontextprotocol/clientCapabilities": map[string]any{}}, "name": name, "arguments": arguments}}
		data, _ := json.Marshal(body)
		req := httptest.NewRequest("POST", "/mcp", bytes.NewReader(data))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("X-Arrdio-Resource", r.PublicURL)
		req.Header.Set("MCP-Protocol-Version", headerVersion)
		req.Header.Set("Mcp-Method", method)
		req.Header.Set("Mcp-Name", name)
		if origin != "" {
			req.Header.Set("Origin", origin)
		}
		w := httptest.NewRecorder()
		r.Handler().ServeHTTP(w, req)
		return w
	}
	w := invoke("server/discover", "", modernMCP, modernMCP, "")
	if w.Code != 200 || !bytes.Contains(w.Body.Bytes(), []byte(`"supportedVersions"`)) {
		t.Fatal(w.Code, w.Body.String())
	}
	w = invoke("tools/call", "identity", modernMCP, modernMCP, "")
	if w.Code != 200 || !bytes.Contains(w.Body.Bytes(), []byte(`"reader"`)) || !bytes.Contains(w.Body.Bytes(), []byte(`"resultType":"complete"`)) {
		t.Fatal(w.Code, w.Body.String())
	}
	w = invoke("tools/call", "identity", modernMCP, legacyMCP, "")
	if w.Code != 400 || !bytes.Contains(w.Body.Bytes(), []byte(`-32020`)) {
		t.Fatal(w.Code, w.Body.String())
	}
	w = invoke("server/discover", "", "2099-01-01", "2099-01-01", "")
	if w.Code != 400 || !bytes.Contains(w.Body.Bytes(), []byte(`-32022`)) {
		t.Fatal(w.Code, w.Body.String())
	}
	w = invoke("tools/call", "identity", modernMCP, modernMCP, "https://attacker.invalid")
	if w.Code != 403 {
		t.Fatal(w.Code)
	}
	w = invoke("tools/call", "enqueue", modernMCP, modernMCP, "")
	if !bytes.Contains(w.Body.Bytes(), []byte(`error`)) && !bytes.Contains(w.Body.Bytes(), []byte(`isError`)) {
		t.Fatal("write tool allowed without acquire scope", w.Body.String())
	}
	counts, e := r.Store.Counts(ctx)
	if e != nil || len(counts) != 0 {
		t.Fatal("read-only grant enqueued work", counts, e)
	}
}
