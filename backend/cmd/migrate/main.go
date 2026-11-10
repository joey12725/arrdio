package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"flag"
	"github.com/ashkaty/arrdio-api/jobs"
	"github.com/ashkaty/arrdio-api/providers"
	"log"
	"os"
)

func main() {
	backup := flag.String("legacy-backup", "", "explicit offline ARRDIO database copy (never a live service DB)")
	snapshot := flag.String("snapshot", "", "new snapshot path for the current application DB")
	flag.Parse()
	path := os.Getenv("ARRDIO_JOBS_DB")
	if path == "" {
		log.Fatal("ARRDIO_JOBS_DB required")
	}
	s, e := jobs.Open(path)
	if e != nil {
		log.Fatal("application database unavailable")
	}
	defer s.Close()
	if *snapshot != "" {
		if e = s.Backup(context.Background(), *snapshot); e != nil {
			log.Fatal("snapshot failed")
		}
		log.Print("Application snapshot complete")
		return
	}
	if *backup == "" {
		log.Fatal("--legacy-backup or --snapshot is required")
	}
	key, e := os.ReadFile(os.Getenv("ARRDIO_ENCRYPTION_KEY_FILE"))
	if encoded := os.Getenv("ARRDIO_ENCRYPTION_KEY"); encoded != "" {
		key, e = base64.StdEncoding.DecodeString(encoded)
	}
	if e != nil {
		log.Fatal("encryption key unavailable")
	}
	runtime, e := jobs.NewRuntime(s, key)
	if e != nil {
		log.Fatal("persistent key or schema invalid")
	}
	source := &providers.ZLibrary{Runtime: runtime}
	runtime.ValidateNewAccount = source.VerifyCredentials
	result, e := runtime.ImportLegacyAccounts(context.Background(), *backup)
	if e != nil {
		log.Fatal("legacy import failed; existing destination data retained")
	}
	json.NewEncoder(os.Stdout).Encode(result)
	if result.Failed > 0 {
		os.Exit(1)
	}
}
