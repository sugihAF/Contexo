package main

import (
	"fmt"
	"log"
	"os"

	"github.com/sugihAF/contexo/internal/auth"
	"github.com/sugihAF/contexo/internal/server"
	"github.com/sugihAF/contexo/internal/server/service"
)

func main() {
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	// For MVP-1, use in-memory store when no DB is configured
	store := service.NewMemStore()
	svc := service.New(store)

	// Simple key validator for MVP
	validator := func(key string) (string, bool) {
		// In production, validate against PostgreSQL api_keys table
		expectedKey := os.Getenv("CTXHUB_API_KEY")
		if expectedKey == "" {
			expectedKey = "dev-key"
		}
		if key == expectedKey {
			return "admin", true
		}
		return "", false
	}

	router := server.NewRouter(svc, auth.KeyValidator(validator))

	log.Printf("CtxHub server starting on :%s", port)
	if err := router.Run(fmt.Sprintf(":%s", port)); err != nil {
		log.Fatal(err)
	}
}
