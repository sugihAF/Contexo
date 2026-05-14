package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/sugihAF/contexo/internal/schema"
	"github.com/sugihAF/contexo/internal/store/pagestore"
)

// ResourceTemplate describes an MCP resource template.
type ResourceTemplate struct {
	URITemplate string                 `json:"uriTemplate"`
	Name        string                 `json:"name"`
	Description string                 `json:"description,omitempty"`
	MimeType    string                 `json:"mimeType,omitempty"`
	Annotations map[string]interface{} `json:"annotations,omitempty"`
}

// HubServer serves CtxHub knowledge pages over MCP from a local .ctxhub/ tree.
type HubServer struct {
	store *pagestore.Store
}

// NewHubServer creates a HubServer backed by the given local pagestore.
func NewHubServer(store *pagestore.Store) *HubServer {
	return &HubServer{store: store}
}

// ListResources returns the resource templates this server publishes.
func (s *HubServer) ListResources() []ResourceTemplate {
	return []ResourceTemplate{
		{
			URITemplate: "ctx://index",
			Name:        "Knowledge Index",
			Description: "Always-loaded index of all pages in the project",
			MimeType:    "text/markdown",
			Annotations: map[string]interface{}{"priority": 1.0},
		},
		{
			URITemplate: "ctx://tags",
			Name:        "Tag Index",
			Description: "Tag → page mapping",
			MimeType:    "text/markdown",
			Annotations: map[string]interface{}{"priority": 0.7},
		},
		{
			URITemplate: "ctx://wiki/{slug}",
			Name:        "Wiki Page",
			Description: "Concept, entity, or analysis page by slug",
			MimeType:    "text/markdown",
			Annotations: map[string]interface{}{"priority": 0.6},
		},
		{
			URITemplate: "ctx://raw/{session-id}",
			Name:        "Raw Session",
			Description: "Raw session capture from raw/sessions/",
			MimeType:    "text/markdown",
			Annotations: map[string]interface{}{"priority": 0.4},
		},
		{
			URITemplate: "ctx://search?q={query}&type={type}&tag={tag}",
			Name:        "Page Search",
			Description: "Search pages by substring, type, or tag",
			MimeType:    "application/json",
			Annotations: map[string]interface{}{"priority": 0.5},
		},
	}
}

// HandleResourceRead dispatches a ctx:// URI to the right backend lookup.
func (s *HubServer) HandleResourceRead(ctx context.Context, uri string) ([]byte, string, error) {
	parsed, err := url.Parse(uri)
	if err != nil {
		return nil, "", fmt.Errorf("mcp: parse uri: %w", err)
	}
	if parsed.Scheme != "ctx" {
		return nil, "", fmt.Errorf("mcp: unsupported scheme: %s", parsed.Scheme)
	}

	path := strings.TrimPrefix(parsed.Host+parsed.Path, "/")
	if path == "" {
		path = parsed.Host
	}

	switch {
	case path == "index":
		return s.readFile("index.md", "text/markdown")

	case path == "tags":
		return s.readFile("tags.md", "text/markdown")

	case strings.HasPrefix(path, "wiki/"):
		slug := strings.TrimPrefix(path, "wiki/")
		return s.readBySlug(slug, []schema.PageType{
			schema.TypeConcept, schema.TypeEntity, schema.TypeAnalysis,
		})

	case strings.HasPrefix(path, "raw/"):
		slug := strings.TrimPrefix(path, "raw/")
		return s.readBySlug(slug, []schema.PageType{schema.TypeSource})

	case path == "search":
		return s.handleSearch(parsed.Query())

	default:
		return nil, "", fmt.Errorf("mcp: unknown resource: %s", uri)
	}
}

func (s *HubServer) readFile(relPath, mimeType string) ([]byte, string, error) {
	data, err := os.ReadFile(filepath.Join(s.store.Root, relPath))
	if err != nil {
		return nil, "", fmt.Errorf("mcp: read %s: %w", relPath, err)
	}
	return data, mimeType, nil
}

func (s *HubServer) readBySlug(slug string, types []schema.PageType) ([]byte, string, error) {
	for _, t := range types {
		fm := schema.PageFrontmatter{Type: t, Slug: slug}
		p, err := s.store.Read(fm.RelPath())
		if err == nil {
			data, err := schema.SerializePage(p)
			if err != nil {
				return nil, "", fmt.Errorf("mcp: serialize: %w", err)
			}
			return data, "text/markdown", nil
		}
		if !errors.Is(err, pagestore.ErrNotFound) {
			return nil, "", err
		}
	}
	return nil, "", fmt.Errorf("mcp: page %q not found", slug)
}

type searchResult struct {
	Slug    string   `json:"slug"`
	Type    string   `json:"type"`
	Path    string   `json:"path"`
	Author  string   `json:"author"`
	Tags    []string `json:"tags,omitempty"`
	Summary string   `json:"summary,omitempty"`
}

func (s *HubServer) handleSearch(params url.Values) ([]byte, string, error) {
	q := strings.ToLower(params.Get("q"))
	typeFilter := params.Get("type")
	tag := params.Get("tag")

	filter := pagestore.Filter{}
	if typeFilter != "" {
		filter.Types = []schema.PageType{schema.PageType(typeFilter)}
	}
	if tag != "" {
		filter.Tags = []string{tag}
	}

	pages, err := s.store.List(filter)
	if err != nil {
		return nil, "", fmt.Errorf("mcp: search list: %w", err)
	}

	results := make([]searchResult, 0, len(pages))
	for _, p := range pages {
		fm := p.Frontmatter
		if q != "" {
			hay := strings.ToLower(fm.Slug + " " + fm.ReasoningSummary + " " + p.Body)
			if !strings.Contains(hay, q) {
				continue
			}
		}
		results = append(results, searchResult{
			Slug:    fm.Slug,
			Type:    string(fm.Type),
			Path:    fm.RelPath(),
			Author:  fm.Author,
			Tags:    fm.Tags,
			Summary: fm.ReasoningSummary,
		})
	}

	data, err := json.Marshal(results)
	if err != nil {
		return nil, "", fmt.Errorf("mcp: marshal search: %w", err)
	}
	return data, "application/json", nil
}
