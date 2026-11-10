package jobs

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// The latest published Go SDK (v1.8) implements the legacy handshake revision.
// This small dual-era transport adds 2026-07-28 request metadata/discovery while
// retaining the SDK's tool schema validation and shared operation handlers.
const modernMCP = "2026-07-28"
const legacyMCP = "2025-11-25"

type capturedResponse struct {
	header http.Header
	body   bytes.Buffer
	status int
}

func (w *capturedResponse) Header() http.Header { return w.header }
func (w *capturedResponse) WriteHeader(status int) {
	if w.status == 0 {
		w.status = status
	}
}
func (w *capturedResponse) Write(b []byte) (int, error) {
	if w.status == 0 {
		w.status = 200
	}
	return w.body.Write(b)
}
func headerValue(value string) (string, error) {
	if strings.HasPrefix(value, "=?base64?") && strings.HasSuffix(value, "?=") {
		data, e := base64.StdEncoding.DecodeString(strings.TrimSuffix(strings.TrimPrefix(value, "=?base64?"), "?="))
		return string(data), e
	}
	return value, nil
}
func (r *Runtime) dualEraMCP(legacy http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, q *http.Request) {
		if origin := q.Header.Get("Origin"); origin != "" {
			public, e := url.Parse(r.PublicURL)
			if e != nil || origin != public.Scheme+"://"+public.Host {
				w.WriteHeader(403)
				return
			}
		}
		if q.Method != "POST" {
			legacy.ServeHTTP(w, q)
			return
		}
		data, e := io.ReadAll(http.MaxBytesReader(w, q.Body, 16<<20))
		if e != nil {
			w.WriteHeader(413)
			return
		}
		q.Body.Close()
		var message struct {
			JSONRPC string                     `json:"jsonrpc"`
			ID      json.RawMessage            `json:"id"`
			Method  string                     `json:"method"`
			Params  map[string]json.RawMessage `json:"params"`
		}
		if json.Unmarshal(data, &message) != nil {
			respond(w, 400, map[string]any{"jsonrpc": "2.0", "error": map[string]any{"code": -32700, "message": "Invalid JSON"}})
			return
		}
		var meta map[string]json.RawMessage
		json.Unmarshal(message.Params["_meta"], &meta)
		var version string
		json.Unmarshal(meta["io.modelcontextprotocol/protocolVersion"], &version)
		modern := version != "" || q.Header.Get("MCP-Protocol-Version") == modernMCP || message.Method == "server/discover"
		if !modern {
			q.Body = io.NopCloser(bytes.NewReader(data))
			legacy.ServeHTTP(w, q)
			return
		}
		rpcError := func(status, code int, msg string, details any) {
			errorBody := map[string]any{"code": code, "message": msg}
			if details != nil {
				errorBody["data"] = details
			}
			body := map[string]any{"jsonrpc": "2.0", "error": errorBody}
			if len(message.ID) > 0 {
				body["id"] = message.ID
			}
			respond(w, status, body)
		}
		if len(message.ID) == 0 || string(message.ID) == "null" {
			message.ID = nil
			rpcError(400, -32600, "Invalid request", nil)
			return
		}
		var identifier any
		idDecoder := json.NewDecoder(bytes.NewReader(message.ID))
		idDecoder.UseNumber()
		validID := idDecoder.Decode(&identifier) == nil
		switch value := identifier.(type) {
		case string:
		case json.Number:
			_, e := value.Int64()
			validID = validID && e == nil
		default:
			validID = false
		}
		if !validID {
			message.ID = nil
			rpcError(400, -32600, "Request ID must be a string or integer", nil)
			return
		}
		if message.JSONRPC != "2.0" {
			rpcError(400, -32600, "Invalid request", nil)
			return
		}
		var capabilities map[string]any
		if version == "" || json.Unmarshal(meta["io.modelcontextprotocol/clientCapabilities"], &capabilities) != nil || capabilities == nil {
			rpcError(400, -32602, "Required per-request metadata is missing", nil)
			return
		}
		if q.Header.Get("MCP-Protocol-Version") != version || q.Header.Get("Mcp-Method") != message.Method {
			rpcError(400, -32020, "Required headers do not match the request", nil)
			return
		}
		if version != modernMCP {
			rpcError(400, -32022, "Unsupported protocol version", map[string]any{"supported": []string{modernMCP, legacyMCP}, "requested": version})
			return
		}
		info := map[string]string{"name": "arrdio", "version": "2.0.0"}
		if message.Method == "server/discover" {
			respond(w, 200, map[string]any{"jsonrpc": "2.0", "id": message.ID, "result": map[string]any{"resultType": "complete", "supportedVersions": []string{modernMCP, legacyMCP}, "capabilities": map[string]any{"tools": map[string]any{}}, "_meta": map[string]any{"io.modelcontextprotocol/serverInfo": info}, "ttlMs": 0, "cacheScope": "public"}})
			return
		}
		if message.Method != "tools/list" && message.Method != "tools/call" {
			rpcError(404, -32601, "Method not found", nil)
			return
		}
		if message.Method == "tools/call" {
			var name string
			json.Unmarshal(message.Params["name"], &name)
			header, e := headerValue(q.Header.Get("Mcp-Name"))
			if e != nil || header != name || name == "" {
				rpcError(400, -32020, "Mcp-Name does not match the request", nil)
				return
			}
		}
		// No callbacks, subscriptions, or MRTR capabilities are advertised. These
		// request/response tools need no client capabilities beyond the required map.
		clone := q.Clone(q.Context())
		clone.Header = q.Header.Clone()
		clone.Header.Set("MCP-Protocol-Version", legacyMCP)
		clone.Header.Set("Accept", "application/json, text/event-stream")
		delete(meta, "io.modelcontextprotocol/protocolVersion")
		delete(meta, "io.modelcontextprotocol/clientCapabilities")
		delete(meta, "io.modelcontextprotocol/clientInfo")
		message.Params["_meta"], _ = json.Marshal(meta)
		dispatchData, _ := json.Marshal(message)
		clone.Body = io.NopCloser(bytes.NewReader(dispatchData))
		clone.ContentLength = int64(len(dispatchData))
		captured := &capturedResponse{header: make(http.Header)}
		legacy.ServeHTTP(captured, clone)
		var response map[string]any
		if json.Unmarshal(captured.body.Bytes(), &response) != nil {
			rpcError(500, -32603, "Tool transport failed", nil)
			return
		}
		status := captured.status
		if result, ok := response["result"].(map[string]any); ok {
			result["resultType"] = "complete"
			result["_meta"] = map[string]any{"io.modelcontextprotocol/serverInfo": info}
		}
		if err, ok := response["error"].(map[string]any); ok {
			if err["code"] == float64(-32601) {
				status = 404
			} else if err["code"] == float64(-32602) {
				status = 400
			}
		}
		respond(w, status, response)
	})
}
