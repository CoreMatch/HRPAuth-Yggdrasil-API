package controllers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/lnb/HRPAuth-Yggdrasil-API/config"
)

// The meta endpoint is the authlib-injector API document for this service.
// Launchers (and authlib-injector's runtime fetch, which the core relay
// serves without the ALI header) need `meta` / `skinDomains` /
// `signaturePublickey` at the top level, while the historical nested
// `yggdrasil` object and the status envelope are kept for existing consumers.
func TestMetaServesFlatAuthlibMetadata(t *testing.T) {
	old := config.AppConfig
	cfg := &config.Config{}
	cfg.Site.Name = "TestSite"
	cfg.Site.Version = "1.2.3"
	cfg.Runtime.SiteURL = "https://ha.example.com"
	cfg.Yggdrasil.Server.Name = "TestYgg"
	cfg.Yggdrasil.Server.Implementation = "TestImpl"
	cfg.Yggdrasil.Server.Version = "9.9.9"
	cfg.Yggdrasil.Server.SkinDomains = []string{"ha.example.com", ".ha.example.com"}
	cfg.Yggdrasil.Server.SignaturePublicKey = "test-public-key"
	config.AppConfig = cfg
	t.Cleanup(func() { config.AppConfig = old })

	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/", nil)

	NewYggdrasilController().Meta(c)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	var body map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}

	// Flat, authlib-injector-shaped fields at the top level.
	if body["signaturePublickey"] != "test-public-key" {
		t.Errorf("top-level signaturePublickey = %v, want test-public-key", body["signaturePublickey"])
	}
	domains, ok := body["skinDomains"].([]interface{})
	if !ok || len(domains) != 2 || domains[0] != "ha.example.com" {
		t.Errorf("top-level skinDomains = %v, want [ha.example.com .ha.example.com]", body["skinDomains"])
	}
	meta, ok := body["meta"].(map[string]interface{})
	if !ok {
		t.Fatalf("top-level meta missing or not an object: %v", body["meta"])
	}
	if meta["serverName"] != "TestYgg" {
		t.Errorf("meta.serverName = %v, want TestYgg", meta["serverName"])
	}

	// Nested form and status envelope preserved for existing consumers.
	nested, ok := body["yggdrasil"].(map[string]interface{})
	if !ok {
		t.Fatalf("nested yggdrasil object missing: %v", body["yggdrasil"])
	}
	if nested["signaturePublickey"] != "test-public-key" {
		t.Errorf("nested signaturePublickey = %v, want test-public-key", nested["signaturePublickey"])
	}
	if body["status"] != "online" {
		t.Errorf("status = %v, want online", body["status"])
	}
}
