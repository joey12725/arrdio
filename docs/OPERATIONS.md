# Configuration, deployment and recovery

## Files

- `compose-example.yaml`: production services for this release. Images are pinned
  to an explicitly published tag; secrets come from two env files outside the
  checkout (one for the API, one for the web server).
- `compose.local.yaml`: isolated build with workers paused, its own volume and a
  loopback-only web port. It mounts no libraries.

The release is **not backward compatible** with the previous images: the API
moves from port 2130 to 2131, the web server no longer reads
`NUXT_PUBLIC_API_BASE`, and the old `/app/data` database is never opened in place.

## Publishing images

Merging does not publish anything. Run the **Build, test and publish** workflow
manually with a version tag (for example `v2.0.0`). It re-runs every check, then
pushes `arrdio-api:<tag>` and `arrdio-frontend:<tag>`. The `latest` and `dev`
tags are refused, because the previous production compose pulls them.

## Cutover

1. Publish a tag as above, then stop and remove the previous `api`/`frontend`
   containers (the new ones reuse their names). Keep their volume and note their
   image digests: together they are the rollback.
2. Create the data directory, owned by the API user (`ARRDIO_UID`/`ARRDIO_GID`).
   Generate the encryption key (`head -c 32 /dev/urandom`), the gateway token and
   the session secret (`openssl rand -base64 48`) into the secret manager.
3. Write the API env file (`ARRDIO_GATEWAY_TOKEN`, `ARRDIO_PUBLIC_URL`, provider
   and library credentials below) and the web env file (`NUXT_ARRDIO_GATEWAY_TOKEN`
   with the same token, `NUXT_SESSION_PASSWORD`, `NUXT_OAUTH_AUTHENTIK_*`).
   The existing Authentik values and `NUXT_OIDC_PROVIDERS_OIDC_*` names work as-is;
   the callback stays `/api/auth/authentik`.
4. Give qBittorrent a dedicated category (for example `arrdio`) whose save path is
   **outside** every library. Mount the client's whole download root read-only
   as the torrent payload, with `QBITTORRENT_PATH` set to the client's path for
   it. A category that saves straight into a library would leave the seeding
   payload and ARRDIO's imported copy side by side.
5. Write `routes.json` with container paths under the library mounts. Set
   `ARRDIO_WEB_BIND`/`ARRDIO_WEB_PORT` to the address the reverse proxy already
   targets, and `ARRDIO_PUBLIC_URL` to `https://<public host>/agent`. Start with
   `ARRDIO_WORKERS=false`. Settings must show every source and library ready.
6. Optional: import accounts from an offline copy of the old database with
   `arrdio-migrate --legacy-backup <copy>` (never the live file).
7. Sign in through Authentik, queue one book, then set `ARRDIO_WORKERS=true` and
   recreate only the API container.

Rollback: stop the new containers, then start the previous images against their
untouched volume. The new data directory can stay in place for a later attempt.

## Removed legacy surface

The legacy route handlers and browser-memory acquisition pages were removed.
Their old API paths return 410 through the authenticated frontend guard (or 404
at the new backend); they cannot bypass the durable queue. The old category and
account pages redirect to Settings, which has no remove-account operation.

## Required server settings

| Variable | Meaning |
| --- | --- |
| `ARRDIO_JOBS_DB` | Absolute application database path in persistent storage |
| `ARRDIO_ENCRYPTION_KEY_FILE` | File containing exactly 32 random bytes; preserve across upgrades |
| `ARRDIO_ENCRYPTION_KEY` | Alternative: base64 encoding of those same 32 bytes from a secret manager |
| `ARRDIO_GATEWAY_TOKEN` | Random secret of at least 32 characters, shared only with Nuxt's server |
| `ARRDIO_PUBLIC_URL` | Stable agent resource URL, e.g. `https://your-private-host/agent` |
| `ARRDIO_LISTEN` | Default `127.0.0.1:2131`; container default `0.0.0.0:2131` on private network |
| `ARRDIO_STAGING` | Persistent staging directory; defaults beside the database |
| `ARRDIO_WORKERS` | Explicit `true` to dispatch; default paused |
| `ARRDIO_ROUTES_FILE` | Operator-approved routing JSON, outside source control |
| `ARRDIO_LIBRARY_OWNER` | `uid:gid` given to every file/folder ARRDIO creates in a library (e.g. `99:100`); needs a root process. Use it when existing library folders belong to root |
| `ARRDIO_ROUTES` | Alternative: the same routing JSON inline (the file wins when both are set) |

A book requested without a category, whose format has several routes, is
classified once (standard Luna) into one of the route categories before it is
placed. Ebook jobs are also offered the manga routes, since Z-Library carries
manga volumes as EPUB and PDF; an ebook classified as manga is checked against
and placed in the manga library with manga naming (`Series/Series v01`).

Nuxt needs `NUXT_ARRDIO_BASE`, `NUXT_ARRDIO_GATEWAY_TOKEN`, a persistent
`NUXT_SESSION_PASSWORD`, and its existing `NUXT_OAUTH_AUTHENTIK_*` values.
Existing `NUXT_OIDC_PROVIDERS_OIDC_*` aliases remain accepted. The callback path
is `/api/auth/authentik`, the same as the previous release, so the Authentik
provider needs no change.

Secrets never belong in Docker build arguments or image layers. Do not print
`docker compose config`; use `config --quiet`. Keep secret files outside the
checkout, owner-readable only. Production shared-media writes must run under the
operator-approved media UID/GID, with matching permissions on the dedicated app
data directory. The image's default non-root UID 10001 suits isolated storage;
shared media folders usually need their own owner (99:100 on Unraid).

## Provider configuration

- Z-Library accounts are added through Settings, validated without `/file`, and
  encrypted in SQLite. Optional `ARRDIO_SEED_ACCOUNTS_FILE` accepts an operator-only
  JSON array of email/password pairs. It does not overwrite existing accounts.
  Intake performs a fresh login plus authenticated profile check with the exact
  submitted credentials. Existing cookies cannot validate a different or revoked
  password. Rejected/unavailable verification stores nothing; zero remaining quota
  does not make otherwise valid credentials invalid. Background profile refreshes
  may reuse a session, but that cache is bound to the verified credential digest.
- `PROWLARR_URL`, `PROWLARR_API_KEY`: discover enabled indexer/category capabilities.
- `QBITTORRENT_URL`, `QBITTORRENT_USERNAME`, `QBITTORRENT_PASSWORD`,
  `QBITTORRENT_CATEGORY`, `ARRDIO_TORRENT_ROOT`, `QBITTORRENT_PATH`: existing
  client, category and read-only payload mount. Torrents are added in automatic
  management mode, so the category's save path decides where payloads land.
  Mount the client's whole download root at `ARRDIO_TORRENT_ROOT` and set
  `QBITTORRENT_PATH` to the client's own path for it (e.g. `/downloads`): files
  are then found wherever the client reports them, including its incomplete
  folder, where qBittorrent keeps a partially selected pack (one manga volume).
  Without `QBITTORRENT_PATH`, `ARRDIO_TORRENT_ROOT` must be the category save path
  and partial packs are not supported. A download that stalls for four hours is
  abandoned for the next candidate; the torrent itself is left in the client. No VPN, tracker, seed-limit or
  shared client settings are changed. New volume-specific jobs are paused until
  file selection is set; existing torrent selections are not rewritten.
- Prowlarr release titles in the `Title by Author [LANG / FORMAT]` form (used by
  MyAnonamouse) supply author, language and container facts. Untagged releases
  stay unknown. Without a language requirement, English releases rank first and
  other languages remain eligible. Searches send only the first credited author;
  a subtitled title that finds nothing is retried with its main title.
- Selection, in order: the requested work (abridged, adapted, summary and
  dramatized editions rank last unless requested; a request without an author
  must contain the title as a phrase), then quality among releases with at least
  10 seeders (M4B over M4A over MP3, EPUB over PDF, CBZ over CBR), then seeders.
  The decision model gets the same policy and chooses among those candidates.
- `ABB_URL`: operator-configured source origin; HTML failures stay provider-local.
- `ABB_USERNAME`, `ABB_PASSWORD`: authenticated member login/profile verification;
  use these instead of a manually copied short-lived cookie. Legacy `ABB_COOKIE`
  remains supported when explicitly configured.
- `SLSKD_URL`, `SLSKD_API_KEY`, `ARRDIO_SOULSEEK_ROOT`: slskd API and its payload mount,
  separate from qBittorrent. A search result subset is not a complete audiobook;
  the adapter obtains the selected directory inventory and checks track coverage.
- `AUDIOBOOKSHELF_URL`, `AUDIOBOOKSHELF_TOKEN`, `AUDIOBOOKSHELF_PUBLIC_URL`;
  `KAVITA_URL`, `KAVITA_AUTH_KEY` (renewable plugin authentication) or `KAVITA_TOKEN`,
  and `KAVITA_PUBLIC_URL`. Public URLs control user-facing library links; API
  addresses may remain internal. An offline/incomplete index is not empty.
- `CLIPROXY_URL` (including `/v1`), `CLIPROXY_KEY`: standard `gpt-6-luna`, default
  service tier, reasoning `none` as accepted by the live probe. No Fast alias or
  usage-billed fallback is substituted.
- `TYPESAFE_URL`, `TYPESAFE_KEY`, `TYPESAFE_MODEL`: bounded choice adapter using
  `/v1/systemone`. Discover the model with the configured account. Missing or
  failed TypeSafe falls back to deterministic ranking with visible degraded state.
- `OPENROUTER_API_KEY`, `OPENROUTER_URL`, `OPENROUTER_MODEL`: configured OpenRouter
  decision route, default `typesafe/jev-router`. This is a routing model plus a
  downstream generator, not the direct TypeSafe Decisions API. The effective model
  is reported; invalid responses/timeouts fall back without caching failure.
- Photo extraction uses CLIProxyAPI when `CLIPROXY_URL`/`CLIPROXY_KEY` are set.
  Without them it sends the same standard `gpt-6-luna` request through the
  OpenRouter key as `openai/gpt-6-luna`, for deployments where the proxy is not on
  the application's network. Settings shows which route is active.

Routing JSON is an array of `{category,format,service,library_id,path}`. Allowed
formats are ebook/audio/manga; services are kavita/audiobookshelf. Paths must be
absolute and writable. Put the approved default for each format first. Users do
not choose paths; agents may assign only configured category names. A category
applies to its compatible representation, not both children indiscriminately.

Placed files follow the libraries' conventions: ebooks as
`Author/Title/Title (Author).epub`, manga as `Series/Series v01.cbz`, a single-file
audiobook as `Author/Title/Title.m4b` (multi-file sets keep numbered source names).
Kavita names PDF and comic series from the file name, which readback matches.
Partial copies and conversions left by a restart are removed on the next attempt.

The optional `/refresh` operation invokes supported library scan APIs. Import
readback remains authoritative: accepted scans and local files are not reported
as available until the configured library indexes the item. Categorizing an
ARRDIO-acquired available item queues placement readback using retained originals.
Library-only items without a retained placement source return a conflict rather
than silently pretending the filesystem was changed. Original imported copies
and seeded payloads are not destructively removed by categorization.

Explicit correction is a separate replacement operation. Its durable placement
manifest permits replacement/retirement only of unchanged copies previously
published by the same request. It preserves original transfer files, seeded/inode-
identical sources, unrelated files and external modifications; conflicts remain
visible. Ordinary cancellation still deletes no library content.

Optional hard requirements (`constraints.language`, narrator, edition, container)
are durable and included in text/photo idempotency and canonical identity. Unknown
source facts do not satisfy them. Verified artifact metadata can reject contradictory
source labels, including EPUB OPF language; English permits regional en-* variants,
while an explicitly requested region remains specific. Do not interpret source
availability, a plausible cover, or a structurally valid file as correct metadata.

## Retries and failed searches

A search that finds nothing marks the job `missing` (shown as "search failed",
with the time of the next search). It is searched again after 1 hour, then 2,
4, 8, 16, 32 and 64 hours, and a last time seven days after the first failure;
if that also finds nothing the job becomes `not_found` and leaves the queue.
Retry, or requesting the title again, starts a fresh seven days. Other stalled
outcomes (provider or library waits, allowance waits, imports the library has
not yet indexed) back off from 1 minute doubling to 1 hour. Untried jobs are
claimed before retries of stalled ones, so failed requests never hold up the
rest of a lane, whatever their priority.

## Recovery and rollback

Persist the **whole application data directory**, the encryption key, gateway and
browser-session secrets, endpoint identity, source mappings and provider credentials.
Browser logout does not revoke agent grants. Grants are random scoped credentials
stored as hashes, with no recurring manual expiration. Owner revocation is explicit.

Schema version 2 adds tables and canonical aliases to the initial version 1 store.
It preserves requests, batches, queue sequence/bump rank, account ciphertext,
reservations, receipts and grants. A newer unknown schema is rejected. The original
upstream `arrdio.db` is not opened or overwritten; its account import requires an
operator-approved offline export, not direct access to a live database.

For a consistent rollback copy: stop this application and wait for connections to
close, then copy its entire data directory and retain the corresponding encryption
key/configuration securely. Alternatively use SQLite's backup API for this app's
database. Never copy only an active `.db` while omitting its WAL. Never operate on
Audiobookshelf, Kavita, Plex or another application's database.

To undo an isolated test, stop/remove only its named containers, keep its named
data volume and use the previous verified image/configuration. Do not replay
batches into the upstream download routes. For an incompatible schema rollback,
restore the matching offline application backup; reconcile all external transfer
identifiers and allowance before dispatching. A live ambiguous intent is retained
and never automatically issued a second `/file` or torrent submission.

## Agent installation

Settings generates a user-bound, single-use, 15-minute bootstrap prompt. Download
`/agent/integration/SKILL.md`, `API.md` and `client.py` together. The resulting grant
persists in the client's secret mechanism or an owner-only configuration file.
Declare non-default `ARRDIO_CONFIG` in an MCP client's server environment; clients
such as Inspector deliberately do not inherit every parent environment variable.

The Go SDK handles tool schemas/operations. A small transport adapter adds the
current 2026-07-28 stateless discovery/metadata contract while retaining legacy
2025-11-25 clients. Header/body mismatches, wrong resources/scopes and invalid
Origins are rejected. Optional subscriptions are not advertised; polling is supported.

## Conversion profile

Audio conversion runs in the bounded audio lane, preserves source bytes, natural
disc/track order, chapter boundaries derived from track durations, title/author,
and supported sidecar artwork. AAC tracks are remuxed where possible; other codecs
use AAC 96 kb/s by default (`ARRDIO_AUDIO_BITRATE` accepts 64k, 96k, 128k, 160k or
192k). MP3-to-AAC is lossy and does not improve quality. Duration is
verified before publishing the M4B. Conversion failure is separate from playable
original availability. Existing library back-conversion is not performed.
