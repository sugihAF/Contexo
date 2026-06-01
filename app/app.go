// Package app boots a Contexo server. It is the public seam the OSS server and
// private (contexo-backend) builds share: both call Run, and the private build
// passes extra Registrars that mount cloud-only routes on the authenticated
// /v1 group. Keeping this package public (not under internal/) lets a separate
// module build a Contexo server without copying the bootstrap or importing
// Contexo internals.
package app

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log"
	"os"
	"path/filepath"

	"github.com/gin-gonic/gin"

	"github.com/sugihAF/contexo/internal/auth"
	"github.com/sugihAF/contexo/internal/server"
	"github.com/sugihAF/contexo/internal/server/gitstore"
	"github.com/sugihAF/contexo/internal/server/handler"
	"github.com/sugihAF/contexo/internal/userstore"
)

// Registrar mounts extra routes on the authenticated /v1 group. The handler
// reads the caller's identity from the gin context set by the auth middleware
// (e.g. c.GetString("user_id")). Only public types cross this boundary, so a
// separate module can implement one without importing Contexo internals.
type Registrar func(v1 *gin.RouterGroup)

// Run boots the Contexo server from environment configuration and blocks
// serving HTTP. extras let a private build add cloud-only routes; the OSS
// server calls Run with none and behaves exactly as before.
func Run(extras ...Registrar) error {
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
		return fmt.Errorf("contexo: mkdir data root: %w", err)
	}

	store, err := gitstore.Open(dataRoot)
	if err != nil {
		return fmt.Errorf("contexo: open gitstore at %s: %w", dataRoot, err)
	}

	users, err := userstore.Open(filepath.Join(dataRoot, "contexo.db"))
	if err != nil {
		return fmt.Errorf("contexo: open userstore: %w", err)
	}
	defer users.Close()

	sessionSecret := os.Getenv("CONTEXO_SESSION_SECRET")
	if sessionSecret == "" {
		// Auto-generate a per-boot secret. Existing sessions invalidate on
		// restart — operators should set CONTEXO_SESSION_SECRET in production
		// for persistent sessions.
		b := make([]byte, 32)
		if _, err := rand.Read(b); err != nil {
			return fmt.Errorf("contexo: gen session secret: %w", err)
		}
		sessionSecret = hex.EncodeToString(b)
		log.Println("contexo: WARNING using ephemeral session secret (set CONTEXO_SESSION_SECRET to persist sessions across restarts)")
	}
	signer, err := auth.NewSessionSigner(sessionSecret)
	if err != nil {
		return fmt.Errorf("contexo: session signer: %w", err)
	}

	var googleVerifier auth.Verifier
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

	routeExtras := make([]func(*gin.RouterGroup), len(extras))
	for i := range extras {
		routeExtras[i] = extras[i]
	}
	router := server.NewRouter(h, resolver, routeExtras...)

	log.Printf("Contexo server starting on :%s (data: %s)", port, dataRoot)
	return router.Run(fmt.Sprintf(":%s", port))
}
