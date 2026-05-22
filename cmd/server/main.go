package main

import (
	"log"

	"cpa-pool-manager/internal/api"
	"cpa-pool-manager/internal/config"
	"cpa-pool-manager/internal/cpa"
	"cpa-pool-manager/internal/engine"
	"cpa-pool-manager/internal/store"
)

func main() {
	cfg := config.Load()
	st, err := store.Open(cfg.StateDBPath)
	if err != nil {
		log.Fatalf("open store: %v", err)
	}
	client := cpa.New(cfg.CPABaseURL, cfg.CPAManagementKey, cfg.RequestTimeoutSeconds)
	eng := engine.New(cfg, st, client)
	eng.StartBackgroundLoop()
	r := api.Router(cfg, eng)
	log.Printf("cpa-pool-manager listening on :%s mode=%s", cfg.AppPort, cfg.AppMode)
	if err := r.Run(":" + cfg.AppPort); err != nil {
		log.Fatal(err)
	}
}
