# ARRDIO — household book acquisition

Request books once; ARRDIO finds the ebook and audiobook, imports them into your
Kavita and Audiobookshelf libraries, and keeps track of every request until it is
there. The Go backend and Nuxt/Vue frontend are a durable rewrite of the original
browser-driven app: batches live in SQLite instead of the browser, and the old
unauthenticated download routes are gone.

Deploy with [`compose-example.yaml`](compose-example.yaml) and the cutover steps in
[configuration and recovery](docs/OPERATIONS.md). Images are published only by a
manual workflow run with an explicit tag; merging does not deploy.

## Product behavior

- One book with independent ebook/audio children; ordinary books default to both.
- Durable SQLite queue, LIFO execution, persistent bumps, idempotent bulk intake,
  bounded workers, transfer intents and recovery.
- Debounced catalog suggestions, immediate free-text intake, photo ingestion,
  title/author sorting, server-side filtering, per-format controls and correction.
- Shared encrypted ebook accounts, profile-derived allowance, rollover and a single
  aggregate quota display. Ordinary users can add accounts, never remove them.
- Provider boundaries for modern Z-Library EAPI, Prowlarr/qBittorrent, ABB and slskd.
  Manga selection uses discovered literature categories and requested volumes.
- API-backed library presence, operator-owned destinations, verified atomic copies,
  preserved source payloads and recoverable M4B normalization.
- Authentik browser sign-in, revocable persistent agent grants, REST, MCP and a
  downloadable Python CLI/skill bundle over the same jobs.

## Develop locally

Requirements: Go 1.27.3 with a C compiler for SQLite, Node 22.21+ (supported LTS),
npm 11.6.2, and FFmpeg/ffprobe for media checks and conversion.

```sh
cd backend
go test -race ./...
go vet ./...
```

```sh
cd frontend
npm ci
npm run typecheck
npm run build
```

See [configuration and recovery](docs/OPERATIONS.md) before starting the service.
The API refuses to start without a durable database location, persistent encryption
key, gateway credential and public endpoint identity. Provider credentials stay
backend-only. Authentik is enforced in development as well as production.

`compose.local.yaml` builds an isolated stack with workers paused, a local-only
web port and its own data volume. It mounts no libraries and changes no
production route. Supply its required secrets through your environment/secret
manager, then validate with `docker compose -f compose.local.yaml config --quiet`.

## Layout

| Path | Purpose |
| --- | --- |
| `backend/jobs/` | Durable store, migrations, scheduling, auth, REST/MCP, integration bundle |
| `backend/providers/` | Provider adapters, matching, file validation, library APIs and conversion |
| `backend/app/` | Operator configuration and service lifecycle |
| `frontend/pages/` | Unified queue, settings, Authentik login |
| `frontend/server/` | Authenticated browser gateway and separately authenticated agent transport |
| `docs/` | Configuration, deployment and recovery |

The newer EAPI contract was adapted from an earlier `arrdio-acquisition` working tree
at documented HEAD `0acf315c5fb6a1d9d1d8b3edf51cad301c4259d8`, including its newer
domain/quota/validation changes. Its SSH gateway, confirmation loop, caches,
credentials and downloaded books were not imported into this repository.
