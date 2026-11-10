package app

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"github.com/ashkaty/arrdio-api/jobs"
	"github.com/ashkaty/arrdio-api/providers"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"
)

func Run() {
	path := os.Getenv("ARRDIO_JOBS_DB")
	if path == "" {
		log.Fatal("ARRDIO_JOBS_DB is required")
	}
	if e := os.MkdirAll(filepath.Dir(path), 0700); e != nil {
		log.Fatal("data directory unavailable")
	}
	key, e := os.ReadFile(os.Getenv("ARRDIO_ENCRYPTION_KEY_FILE"))
	if encoded := os.Getenv("ARRDIO_ENCRYPTION_KEY"); encoded != "" {
		key, e = base64.StdEncoding.DecodeString(encoded)
	}
	if e != nil || len(key) != 32 {
		log.Fatal("Configure a persistent 32-byte account encryption key through the secret manager")
	}
	token := os.Getenv("ARRDIO_GATEWAY_TOKEN")
	if len(token) < 32 {
		log.Fatal("ARRDIO_GATEWAY_TOKEN must contain at least 32 characters")
	}
	s, e := jobs.Open(path)
	if e != nil {
		log.Fatal("job database unavailable")
	}
	defer s.Close()
	r, e := jobs.NewRuntime(s, key)
	if e != nil {
		log.Fatal("runtime migration failed")
	}
	r.GatewayKey = token
	r.PublicURL = os.Getenv("ARRDIO_PUBLIC_URL")
	if r.PublicURL == "" {
		log.Fatal("ARRDIO_PUBLIC_URL is required")
	}
	r.Staging = os.Getenv("ARRDIO_STAGING")
	if r.Staging == "" {
		r.Staging = filepath.Join(filepath.Dir(path), "staging")
	}
	r.Enabled = os.Getenv("ARRDIO_WORKERS") == "true"
	r.SearchCatalog = providers.Catalog
	r.Enrich = providers.Enrich
	z := &providers.ZLibrary{Runtime: r}
	r.ValidateAccount = z.Profile
	r.ValidateNewAccount = z.VerifyCredentials
	models := &providers.Models{ProxyURL: os.Getenv("CLIPROXY_URL"), ProxyKey: os.Getenv("CLIPROXY_KEY"), LunaModel: "gpt-6-luna", Reasoning: "none", JevURL: os.Getenv("TYPESAFE_URL"), JevKey: os.Getenv("TYPESAFE_KEY"), JevModel: os.Getenv("TYPESAFE_MODEL")}
	models.RouterKey = os.Getenv("OPENROUTER_API_KEY")
	models.RouterURL = os.Getenv("OPENROUTER_URL")
	if models.RouterURL == "" {
		models.RouterURL = "https://openrouter.ai/api/v1"
	}
	models.RouterModel = os.Getenv("OPENROUTER_MODEL")
	if models.RouterModel == "" {
		models.RouterModel = "typesafe/jev-router"
	}
	r.ExtractPhoto = models.Extract
	r.DecideCandidate = models.Decide
	r.ClassifyCategory = models.Classify
	r.ConvertAudio = providers.NormalizeM4B
	r.VerifyLocal = providers.VerifyRetained
	r.VerifyArtifact = providers.VerifyArtifact
	r.ModelStatus = models.Status
	torrent := &providers.Torrent{URL: os.Getenv("QBITTORRENT_URL"), Username: os.Getenv("QBITTORRENT_USERNAME"), Password: os.Getenv("QBITTORRENT_PASSWORD"), Category: os.Getenv("QBITTORRENT_CATEGORY"), DownloadRoot: os.Getenv("ARRDIO_TORRENT_ROOT"), ClientRoot: os.Getenv("QBITTORRENT_PATH")}
	torrent.Instance = r.InstanceID
	p := &providers.Prowlarr{URL: os.Getenv("PROWLARR_URL"), Key: os.Getenv("PROWLARR_API_KEY"), Torrent: torrent}
	abb := &providers.ABB{URL: os.Getenv("ABB_URL"), Cookie: os.Getenv("ABB_COOKIE"), Torrent: torrent}
	abb.Username = os.Getenv("ABB_USERNAME")
	abb.Password = os.Getenv("ABB_PASSWORD")
	// Torrents first: a book on MyAnonamouse saves a Z-Library download.
	r.Sources = []jobs.Source{p, z, abb}
	r.Sources = append(r.Sources, &providers.Soulseek{URL: os.Getenv("SLSKD_URL"), Key: os.Getenv("SLSKD_API_KEY"), DownloadRoot: os.Getenv("ARRDIO_SOULSEEK_ROOT")})
	for name, key := range map[string]string{"prowlarr": "PROWLARR_URL", "abb": "ABB_URL", "soulseek": "SLSKD_API_KEY"} {
		state := "configured; not probed"
		if os.Getenv(key) == "" {
			state = "not configured"
		}
		r.SetService(name, state)
	}
	if owner := os.Getenv("ARRDIO_LIBRARY_OWNER"); owner != "" {
		if providers.ParseLibraryOwner(owner) != nil {
			log.Fatal("ARRDIO_LIBRARY_OWNER must be uid:gid")
		}
	}
	library := &providers.Libraries{ABSURL: os.Getenv("AUDIOBOOKSHELF_URL"), ABSToken: os.Getenv("AUDIOBOOKSHELF_TOKEN"), KavitaURL: os.Getenv("KAVITA_URL"), KavitaToken: os.Getenv("KAVITA_TOKEN")}
	library.KavitaKey = os.Getenv("KAVITA_AUTH_KEY")
	library.ABSPublicURL = os.Getenv("AUDIOBOOKSHELF_PUBLIC_URL")
	library.KavitaPublicURL = os.Getenv("KAVITA_PUBLIC_URL")
	inline := os.Getenv("ARRDIO_ROUTES")
	if file := os.Getenv("ARRDIO_ROUTES_FILE"); file != "" || inline != "" {
		routes, e := providers.ParseRoutes([]byte(inline))
		if file != "" {
			routes, e = providers.LoadRoutes(file)
		}
		if e != nil {
			log.Fatal("routing configuration invalid")
		}
		library.Routes = routes
		for _, v := range routes {
			if r.ConfigureRoute(context.Background(), v.Category, v.Format, v.Service, v.LibraryID, v.Path) != nil {
				log.Fatal("route persistence failed")
			}
		}
	} else {
		routes, e := r.StoredRoutes(context.Background())
		if e != nil {
			log.Fatal("persisted routing unavailable")
		}
		for _, v := range routes {
			library.Routes = append(library.Routes, providers.Route{Category: v.Category, Format: v.Format, Service: v.Service, LibraryID: v.LibraryID, Path: v.Path})
		}
	}
	r.Library = library
	if file := os.Getenv("ARRDIO_SEED_ACCOUNTS_FILE"); file != "" {
		data, e := os.ReadFile(file)
		if e != nil {
			log.Fatal("account seed unavailable")
		}
		var accounts []struct {
			Email    string `json:"email"`
			Password string `json:"password"`
		}
		if json.Unmarshal(data, &accounts) != nil {
			log.Fatal("invalid account seed")
		}
		for _, a := range accounts {
			if r.AddAccount(context.Background(), a.Email, a.Password) != nil {
				log.Print("An account could not be validated; repair via Settings")
			}
		}
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go r.Run(ctx)
	go func() {
		probe := func() {
			for _, source := range r.Sources {
				checker, ok := source.(interface{ Probe(context.Context) error })
				if !ok {
					continue
				}
				probeCtx, cancel := context.WithTimeout(ctx, 45*time.Second)
				e := checker.Probe(probeCtx)
				cancel()
				if e != nil {
					if e.Error() == "not configured" {
						r.SetService(source.Name(), "not configured")
					} else {
						r.SetService(source.Name(), "unavailable; authentication or connectivity check failed")
					}
				} else {
					r.SetService(source.Name(), "ready; authentication verified")
				}
			}
		}
		probe()
		timer := time.NewTicker(5 * time.Minute)
		defer timer.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-timer.C:
				probe()
			}
		}
	}()
	addr := os.Getenv("ARRDIO_LISTEN")
	if addr == "" {
		addr = "127.0.0.1:2131"
	}
	server := &http.Server{Addr: addr, Handler: r.Handler(), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 30 * time.Second, WriteTimeout: 120 * time.Second, IdleTimeout: 60 * time.Second}
	go func() {
		<-ctx.Done()
		c, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		server.Shutdown(c)
	}()
	log.Printf("ARRDIO listening on %s; workers=%t", addr, r.Enabled)
	if e := server.ListenAndServe(); e != nil && e != http.ErrServerClosed {
		log.Fatal("HTTP listener failed")
	}
}
