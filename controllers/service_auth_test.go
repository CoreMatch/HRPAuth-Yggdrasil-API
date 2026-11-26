package controllers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/lnb/HRPAuth-Yggdrasil-API/clients"
)

func TestResolveServiceBearerScope(t *testing.T) {
	gin.SetMode(gin.TestMode)
	tests := []struct {
		name       string
		token      string
		isService  bool
		scopes     []string
		wantStatus int
	}{
		{"missing", "", true, []string{"migration.commit.as-service"}, http.StatusUnauthorized},
		{"user token", "token", false, []string{"migration.commit.as-service"}, http.StatusForbidden},
		{"missing scope", "token", true, []string{"migration.reconcile.as-service"}, http.StatusForbidden},
		{"allowed", "token", true, []string{"migration.commit.as-service"}, http.StatusOK},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_ = json.NewEncoder(w).Encode(map[string]any{
					"success": true,
					"data": map[string]any{
						"is_service": test.isService,
						"scopes":     test.scopes,
						"uuid":       "0123456789abcdef0123456789abcdef",
					},
				})
			}))
			defer server.Close()
			client := &clients.CoreClient{BaseURL: server.URL, HTTPClient: server.Client()}
			router := gin.New()
			router.POST("/internal/migrations/commit", func(c *gin.Context) {
				if _, ok := resolveServiceBearer(c, client, "migration.commit.as-service"); ok {
					respondOK(c, "ok", nil)
				}
			})
			request := httptest.NewRequest(http.MethodPost, "/internal/migrations/commit", nil)
			if test.token != "" {
				request.Header.Set("Authorization", "Bearer "+test.token)
			}
			response := httptest.NewRecorder()
			router.ServeHTTP(response, request)
			if response.Code != test.wantStatus {
				t.Fatalf("status = %d, want %d: %s", response.Code, test.wantStatus, response.Body.String())
			}
		})
	}
}
