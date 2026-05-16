package main

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log"
	"os"
	"path/filepath"

	"github.com/sugihAF/contexo/internal/auth"
	"github.com/sugihAF/contexo/internal/server"
	"github.com/sugihAF/contexo/internal/server/gitstore"
	"github.com/sugihAF/contexo/internal/server/handler"
	"github.com/sugihAF/contexo/internal/userstore"
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
	if err := os.MkdirAll(dataRoot, 0o755); err != nil {
		log.Fatalf("contexo: mkdir data root: %v", err)
	}

	store, err := gitstore.Open(dataRoot)
	if err != nil {
		log.Fatalf("contexo: open gitstore at %s: %v", dataRoot, err)
	}

	users, err := userstore.Open(filepath.Join(dataRoot, "contexo.db"))
	if err != nil {
		log.Fatalf("contexo: open userstore: %v", err)
	}
	defer users.Close()

	sessionSecret := os.Getenv("CONTEXO_SESSION_SECRET")
	if sessionSecret == "" {
		// Auto-generate a per-boot secret. Existing sessions invalidate on
		// restart — operators should set CONTEXO_SESSION_SECRET in
		// production for persistent sessions.
		b := make([]byte, 32)
		if _, err := rand.Read(b); err != nil {
			log.Fatalf("contexo: gen session secret: %v", err)
		}
		sessionSecret = hex.EncodeToString(b)
		log.Println("contexo: WARNING using ephemeral session secret (set CONTEXO_SESSION_SECRET to persist sessions across restarts)")
	}
	signer, err := auth.NewSessionSigner(sessionSecret)
	if err != nil {
		log.Fatalf("contexo: session signer: %v", err)
	}

	var googleVerifier *auth.GoogleVerifier
	if clientID := os.Getenv("GOOGLE_OAUTH_CLIENT_ID"); clientID != "" {
		googleVerifier = auth.NewGoogleVerifier(clientID)
		log.Printf("contexo: google sign-in enabled (client_id=%s...)", clientID[:min(len(clientID), 12)])
	} else {
		log.Println("contexo: GOOGLE_OAUTH_CLIENT_ID unset — POST /v1/auth/google will return 503")
	}

	legacyKey := os.Getenv("CONTEXO_API_KEY")
	if legacyKey == "" {
		legacyKey = "dev-key"
	}

	resolver := auth.NewResolver(signer, users, legacyKey)
	h := handler.New(store, users, signer, googleVerifier)
	router := server.NewRouter(h, resolver)

	log.Printf("Contexo server starting on :%s (data: %s)", port, dataRoot)
	if err := router.Run(fmt.Sprintf(":%s", port)); err != nil {
		log.Fatal(err)
	}
}
