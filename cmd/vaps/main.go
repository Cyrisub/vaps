package main

import (
	"log"
	"net/http"

	"vaps/internal/appconfig"
	"vaps/internal/blobstore"
	"vaps/internal/httpapi"
	"vaps/internal/metadata"
)

func main() {
	cfg, err := appconfig.FromArgs(nil)
	if err != nil {
		log.Fatal(err)
	}

	meta, err := metadata.Open(cfg.MetadataDB)
	if err != nil {
		log.Fatal(err)
	}
	defer meta.Close()

	handler := httpapi.NewWithMetadata(blobstore.New(cfg.DataDir), meta)
	log.Printf("vaps listening on %s with data dir %s and metadata db %s", cfg.Addr, cfg.DataDir, cfg.MetadataDB)
	if err := http.ListenAndServe(cfg.Addr, handler); err != nil {
		log.Fatal(err)
	}
}
