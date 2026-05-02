package main

import (
	"log"
	"net/http"

	"vaps/internal/appconfig"
	"vaps/internal/blobstore"
	"vaps/internal/httpapi"
)

func main() {
	cfg, err := appconfig.FromArgs(nil)
	if err != nil {
		log.Fatal(err)
	}

	handler := httpapi.New(blobstore.New(cfg.DataDir))
	log.Printf("vaps listening on %s with data dir %s", cfg.Addr, cfg.DataDir)
	if err := http.ListenAndServe(cfg.Addr, handler); err != nil {
		log.Fatal(err)
	}
}
