# API and persistence

All private requests use `Authorization: Bearer <grant>` and
`X-Arrdio-Resource: <exact service URL>`. Browser sessions use a separate trusted
server gateway. Agents cannot add/remove accounts or create other pairings.

* `GET /me`, `/status`, `/quota`, `/routing`, `/requests?limit=50&offset=0&sort=queue`
* `POST /requests`, header `Idempotency-Key`, body `{ "items": [...] }`
* `POST /requests/{id}/bump` (optional `format=ebook`, audio or manga for one representation)
* `POST /requests/{id}/cancel?format=ebook` (omit format for whole parent)
* `POST /requests/{id}/retry?format=audio`
* `POST /categorize`, body `{ "ids": [1,2], "category": "configured-category" }`
* `POST /refresh`, body `{ "id": 1, "format": "ebook" }`; accepted refresh is not availability
* `GET /candidates?id=1&format=ebook`, then optional `POST /correct` with id, format, candidate
  is an explicit replacement operation: it may replace/retire only this request's
  unchanged, app-owned library copies. Original downloads, seeded payloads and
  unrelated or externally modified files are preserved. Active transfers or
  placement return a conflict rather than being silently replaced.
* `GET /catalog?q=title+author`
* `POST /photos`, body `{ "mime": "image/jpeg", "data": "base64" }`; optional name,
  formats and kind override the normal extraction defaults. Poll GET /photos.
  MCP exposes the same operations as import_photo and photos. Photo names are
  ordinary upload management, not per-book crop/evidence tracking.
* `POST /mcp`: MCP Streamable HTTP, modern 2026-07-28 and legacy 2025-11-25.
  The portable adapter adds modern mirrored headers automatically. Direct modern
  HTTP clients must supply protocolVersion/clientCapabilities metadata and matching
  MCP-Protocol-Version, Mcp-Method and (for tool calls) Mcp-Name headers.

Input fields: title, author, catalog_id, kind (book/manga), volume, formats
(ebook/audio/manga). Selection pins catalog_id. Metadata is not proof of source
availability. A replayed idempotency key cannot change the request body.
Optional `constraints` has language, narrator, edition and container string fields.
Non-empty values are hard requirements; narrator applies to audio. Blank values
keep convenient defaults. Unknown source facts cannot satisfy a hard requirement.
Use format-specific requests when a required container only applies to one format.
Constraint-specific requests are not silently merged into unconstrained jobs.
Mixed batches return accepted request IDs and indexed validation errors; valid
entries are retained. Retry the original key/body after an ambiguous response.

HTTP 401 means a missing/revoked credential or wrong resource. Re-pair only after
checking persisted configuration; browser logout is unrelated to agent grants.
HTTP 409 means an idempotency key was reused for different content. Unknown library
presence is not absence. Import pending means wait for library readback.

The CLI's `request METHOD PATH` command reads JSON from stdin for mutation calls,
providing the complete REST fallback. Owner-only pairing:
`POST /pair` with bootstrap, resource, label returns a persistent token once.
Download/install this file and client.py together with SKILL.md.
