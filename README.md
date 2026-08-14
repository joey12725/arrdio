# Arrdio Docker

Arrdio is a two-service app for searching and downloading audiobooks and ebooks from one interface.

- The `frontend/` app is a Nuxt 3 UI that lets you queue titles, search for audiobook and ebook matches, manage download categories, and manage Z-Library users.
- The `backend/` app is a Go API that talks to external services including AudiobookBay, Z-Library, qBittorrent, Open Library, and Prowlarr.
- A sample Docker Compose file is included at [compose-example.yaml](compose-example.yaml).

## What It Does

From the main page, you can:

- add one or more titles
- search for audiobook matches
- search for ebook matches
- send audiobook magnet links to qBittorrent
- download ebooks directly through Z-Library
- assign each item to a download category before downloading

The app also includes:

- a category management page for mapping category names to save paths
- a Z-Library user management page with per-account download quota tracking
- optional Authentik-based login on the frontend

## Project Layout

```text
.
|-- backend/                 Go API
|   |-- main.go              mounts API routes on port 2130
|   |-- handlers/
|   |   |-- adb/             AudiobookBay search via Playwright
|   |   |-- prowlarr/        audiobook search through Prowlarr
|   |   |-- qbit/            add torrents to qBittorrent
|   |   |-- openlibrary/     Open Library search
|   |   `-- zlib/            Z-Library search, user storage, downloads
|   `-- services/database.go SQLite schema bootstrap
|-- frontend/                Nuxt 3 application
|   |-- pages/
|   |   |-- index.vue        main search/download workflow
|   |   |-- category.vue     download category management
|   |   |-- zlibusers.vue    Z-Library user management
|   |   `-- auth/login.vue   Authentik login page
|   `-- server/api/          frontend server routes that proxy to the Go API
`-- .github/workflows/       Docker image build/push workflow
```

## Stack

- Frontend: Nuxt 3, Vue 3, Nuxt UI, Tailwind
- Backend: Go 1.24, Chi router, SQLite
- Browser automation: Playwright
- Containerization: Docker multi-stage builds for both services
- CI: GitHub Actions builds and pushes Docker images on `master` and `dev`

## Services And Ports

- Frontend: `http://localhost:3000`
- Backend API: `http://localhost:2130`

The sample compose file connects the frontend to the backend with:

```yaml
NUXT_PUBLIC_API_BASE: http://api:2130
```

## Docker Setup

1. Copy [compose-example.yaml](compose-example.yaml) to your own compose file such as `compose.yaml`.
2. Replace all blank OIDC values with real values if you want login enabled.
3. Update the host ebook output mount:

```yaml
- /c/Users/your-user/Desktop/output:/ebooks:rw
```

4. Make sure any download directories used by your categories exist and are writable inside the container.
5. Start the stack:

```bash
docker compose up --build
```

## Local Development

### Backend

The backend uses SQLite and creates tables automatically on startup.

```bash
cd backend
go run .
```

Notes:

- the API listens on port `2130`
- the database file is currently `backend/arrdio.db`
- Playwright browsers are installed during startup in `main.go`

### Frontend

```bash
cd frontend
yarn install
yarn dev
```

The frontend expects a backend base URL from either:

- `NUXT_PUBLIC_API_BASE`
- `GOLANG_API_URL`

Example local `.env` values:

```env
NUXT_PUBLIC_API_BASE=http://localhost:2130

# Optional Authentik / OIDC config
NUXT_OAUTH_AUTHENTIK_CLIENT_ID=your-client-id
NUXT_OAUTH_AUTHENTIK_CLIENT_SECRET=your-client-secret
NUXT_OAUTH_AUTHENTIK_DOMAIN=https://your-authentik.example.com
NUXT_OAUTH_AUTHENTIK_REDIRECT_URL=http://localhost:3000/auth/authentik
```

## Main API Routes

Mounted by the Go server:

- `/abb` for AudiobookBay search
- `/qbit` for qBittorrent torrent submission
- `/zlib` for Z-Library user management, search, downloads, and categories
- `/openlibrary` for Open Library search
- `/prowlarr` for Prowlarr audiobook search

Some notable endpoints:

- `POST /abb/searchbooks`
- `POST /prowlarr/searchbooks`
- `POST /qbit/addtorrents`
- `POST /zlib/searchbook`
- `POST /zlib/downloadbook`
- `PUT /zlib/putcategory`
- `GET /zlib/getcategory`
- `PUT /zlib/putzlibuser`
- `GET /zlib/getzlibuser`

## Persistence

The backend initializes these SQLite tables on startup:

- `categories`
- `zlibuser`
- `downloadclient`

Default categories are inserted automatically:

- `audiobooks -> /audiobooks`
- `ebooks -> /ebooks`

## Authentication

The frontend includes an Authentik login route at `/auth/login` and an OAuth callback handler under `frontend/server/api/auth/authentik.get.ts`.

If you do not want authentication yet, you can still run the app locally by setting the API base URL and leaving auth integration out of your flow. The current repo does not include a full top-level auth guard, so authentication is best treated as optional/in-progress.

## CI/CD

GitHub Actions builds and pushes Docker images from `.github/workflows/docker-image.yml`.

- pushes to `dev` publish `dev`
- pushes to `master` or `main` publish `latest`

Current image names:

- `ashwinrameshdocker/arrdio-frontend`
- `ashwinrameshdocker/arrdio-api`

## Current Caveats

These are worth knowing before deployment:

- The backend currently contains hardcoded credentials and hostnames for AudiobookBay, qBittorrent, and Prowlarr in source code. Those should be moved to environment variables before any real deployment.
- `frontend/.env` in the repo contains real-looking secrets. Treat those as compromised and rotate them if they are active anywhere.
- The backend Dockerfile exposes port `8080`, but the Go server actually listens on `2130`.
- The backend opens SQLite with `arrdio.db`, while the compose file mounts `/app/data`. As written, the DB path is not aligned with that mounted volume.
- The repository contains generated frontend artifacts like `.nuxt/` and installed dependencies in `node_modules/`, which usually should not be committed unless intentionally needed.
- The default compose file only shows an `/ebooks` bind mount. If you want audiobook files saved to a mounted host path too, you will likely need to add that mapping and update categories accordingly.

## Suggested Next Cleanup

- move third-party credentials to environment variables
- align the backend database path with the Docker volume
- align the backend Dockerfile exposed port with the actual server port
- add a real root-level compose file and a checked-in `.env.example`
- remove committed generated artifacts if they are not intentional

## License

No license file is currently present in this repository.
