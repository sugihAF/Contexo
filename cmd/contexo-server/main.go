package main

import (
	"fmt"
	"log"
	"os"
	"path/filepath"

	"github.com/sugihAF/contexo/internal/auth"
	"github.com/sugihAF/contexo/internal/server"
	"github.com/sugihAF/contexo/internal/server/gitstore"
)

func main() {
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	dataRoot := os.Getenv("CONTEXO_DATA_ROOT")
	if dataRoot == "" {
		cwd, _ := os.Getwd()
		dataRoot = filepath.Join(cwd, "contexo-data")
	}

	store, err := gitstore.Open(dataRoot)
	if err != nil {
		log.Fatalf("contexo: open gitstore at %s: %v", dataRoot, err)
	}

	validator := func(key string) (string, bool) {
		expectedKey := os.Getenv("CONTEXO_API_KEY")
		if expectedKey == "" {
			expectedKey = "dev-key"
		}
		if key == expectedKey {
			return "admin", true
		}
		return "", false
	}

	router := server.NewRouter(store, auth.KeyValidator(validator))

	log.Printf("Contexo server starting on :%s (data: %s)", port, dataRoot)
	if err := router.Run(fmt.Sprintf(":%s", port)); err != nil {
		log.Fatal(err)
	}
}
