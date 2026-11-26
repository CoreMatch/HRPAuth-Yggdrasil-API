package controllers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/lnb/HRPAuth-Yggdrasil-API/clients"
	"github.com/lnb/HRPAuth-Yggdrasil-API/services"
)

type controllerMigrationRollbacker struct {
	calls  int
	id     string
	status services.MigrationStatus
	err    error
}

func (r *controllerMigrationRollbacker) Rollback(ctx context.Context, migrationID string) (services.MigrationStatus, error) {
	r.calls++
	r.id = migrationID
	return r.status, r.err
}

func setupMigrationRollbackController(t *testing.T, rollbacker *controllerMigrationRollbacker, scope string) (*gin.Engine, *int) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	coreCalls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		coreCalls++
		_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "data": map[string]any{
			"is_service": true, "scopes": []string{scope},
		}})
	}))
	t.Cleanup(server.Close)
	core := &clients.CoreClient{BaseURL: server.URL, HTTPClient: server.Client()}
	router := gin.New()
	router.Use(RequestIDMiddleware())
	router.POST("/internal/migrations/:migration_id/rollback", NewMigrationRollbackController(core, rollbacker).Rollback)
	return router, &coreCalls
}

func TestMigrationRollbackControllerRollback(t *testing.T) {
	rollbacker := &controllerMigrationRollbacker{status: services.MigrationStatus{
		MigrationID: controllerMigrationID, Status: "rolled_back", UpdatedAt: time.Now().UTC(),
		Resources: []services.MigrationResource{{Kind: "profile", ResourceID: controllerProfileID, Ownership: "created_by_migration", Present: false}},
	}}
	router, coreCalls := setupMigrationRollbackController(t, rollbacker, "migration.rollback.as-service")
	request := httptest.NewRequest(http.MethodPost, "/internal/migrations/"+controllerMigrationID+"/rollback", nil)
	request.Header.Set("Authorization", "Bearer service-token")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusOK || rollbacker.calls != 1 || rollbacker.id != controllerMigrationID || *coreCalls != 1 {
		t.Fatalf("status=%d calls=%d id=%s core=%d body=%s", response.Code, rollbacker.calls, rollbacker.id, *coreCalls, response.Body.String())
	}
}

func TestMigrationRollbackControllerRequiresRollbackScope(t *testing.T) {
	rollbacker := &controllerMigrationRollbacker{}
	router, _ := setupMigrationRollbackController(t, rollbacker, "migration.reconcile.as-service")
	request := httptest.NewRequest(http.MethodPost, "/internal/migrations/"+controllerMigrationID+"/rollback", nil)
	request.Header.Set("Authorization", "Bearer service-token")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusForbidden || rollbacker.calls != 0 {
		t.Fatalf("status=%d calls=%d body=%s", response.Code, rollbacker.calls, response.Body.String())
	}
}

func TestMigrationRollbackControllerRequiresServiceToken(t *testing.T) {
	rollbacker := &controllerMigrationRollbacker{}
	router, coreCalls := setupMigrationRollbackController(t, rollbacker, "migration.rollback.as-service")
	request := httptest.NewRequest(http.MethodPost, "/internal/migrations/"+controllerMigrationID+"/rollback", nil)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized || rollbacker.calls != 0 || *coreCalls != 0 {
		t.Fatalf("status=%d calls=%d core=%d", response.Code, rollbacker.calls, *coreCalls)
	}
}

func TestMigrationRollbackControllerMapsErrors(t *testing.T) {
	tests := []struct {
		code   string
		status int
	}{
		{"invalid_request", http.StatusBadRequest},
		{"migration_not_found", http.StatusNotFound},
		{"migration_invalid_state", http.StatusConflict},
		{"migration_rollback_not_allowed", http.StatusConflict},
		{"migration_rollback_failed", http.StatusInternalServerError},
	}
	for _, test := range tests {
		t.Run(test.code, func(t *testing.T) {
			rollbacker := &controllerMigrationRollbacker{err: &services.MigrationError{Code: test.code, Stage: "rollback", Err: errors.New("upstream detail")}}
			router, _ := setupMigrationRollbackController(t, rollbacker, "migration.rollback.as-service")
			request := httptest.NewRequest(http.MethodPost, "/internal/migrations/"+controllerMigrationID+"/rollback", nil)
			request.Header.Set("Authorization", "Bearer service-token")
			response := httptest.NewRecorder()
			router.ServeHTTP(response, request)
			if response.Code != test.status || !strings.Contains(response.Body.String(), test.code) || strings.Contains(response.Body.String(), "upstream detail") {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
		})
	}
}
