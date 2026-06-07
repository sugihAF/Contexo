package app

import (
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/sugihAF/contexo/quota"
)

func TestOptions_CollectRegistrarsAndQuota(t *testing.T) {
	noop := func(*gin.RouterGroup) {}

	// A nil registrar is ignored; real ones accumulate; WithQuota sets policy.
	cfg := collectOptions(
		WithRegistrar(noop),
		WithRegistrar(nil),
		WithQuota(quota.Unlimited{}),
	)
	if len(cfg.registrars) != 1 {
		t.Errorf("registrars = %d, want 1 (nil skipped)", len(cfg.registrars))
	}
	if cfg.quota == nil {
		t.Error("expected quota policy to be set")
	}
}

func TestOptions_DefaultsAreEmpty(t *testing.T) {
	cfg := collectOptions()
	if len(cfg.registrars) != 0 {
		t.Errorf("expected no registrars by default, got %d", len(cfg.registrars))
	}
	if len(cfg.rootRegistrars) != 0 {
		t.Errorf("expected no root registrars by default, got %d", len(cfg.rootRegistrars))
	}
	if cfg.quota != nil {
		t.Error("expected nil quota by default (Run falls back to the handler's Unlimited)")
	}
}

func TestOptions_CollectRootRegistrars(t *testing.T) {
	noop := func(*gin.RouterGroup) {}
	// Root registrars mount unauthenticated routes (e.g. the Stripe webhook).
	cfg := collectOptions(WithRootRegistrar(noop), WithRootRegistrar(nil), WithRootRegistrar(noop))
	if len(cfg.rootRegistrars) != 2 {
		t.Errorf("rootRegistrars = %d, want 2 (nil skipped)", len(cfg.rootRegistrars))
	}
}
