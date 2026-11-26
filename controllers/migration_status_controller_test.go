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

type controllerMigrationReconciler struct {
	calls  int
	id     string
	status services.MigrationStatus
	err    error
}

func (r *controllerMigrationReconciler) Reconcile(ctx context.Context, migrationID string) (services.MigrationStatus, error) {
	r.calls++
	r.id = migrationID
	return r.status, r.err
}

func setupMigrationStatusController(t *testing.T, reconciler *controllerMigrationReconciler, scope string) (*gin.Engine, *int) {
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
	router.POST("/internal/migrations/:migration_id/reconcile", NewMigrationStatusController(core, reconciler).Reconcile)
	return router, &coreCalls
}

func TestMigrationStatusControllerReconcile(t *testing.T) {
	reconciler := &controllerMigrationReconciler{status: services.MigrationStatus{
		MigrationID: controllerMigrationID, Status: "failed", UpdatedAt: time.Now().UTC(),
		Resources: []services.MigrationResource{{Kind: "profile", ResourceID: controllerProfileID, Ownership: "created_by_migration", Present: false}},
	}}
	router, coreCalls := setupMigrationStatusController(t, reconciler, "migration.reconcile.as-service")
	request := httptest.NewRequest(http.MethodPost, "/internal/migrations/"+controllerMigrationID+"/reconcile", nil)
	request.Header.Set("Authorization", "Bearer service-token")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusOK || reconciler.calls != 1 || reconciler.id != controllerMigrationID || *coreCalls != 1 {
		t.Fatalf("status=%d calls=%d id=%s core=%d body=%s", response.Code, reconciler.calls, reconciler.id, *coreCalls, response.Body.String())
	}
}

func TestMigrationStatusControllerRequiresReconcileScope(t *testing.T) {
	reconciler := &controllerMigrationReconciler{}
	router, _ := setupMigrationStatusController(t, reconciler, "migration.commit.as-service")
	request := httptest.NewRequest(http.MethodPost, "/internal/migrations/"+controllerMigrationID+"/reconcile", nil)
	request.Header.Set("Authorization", "Bearer service-token")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusForbidden || reconciler.calls != 0 {
		t.Fatalf("status=%d calls=%d body=%s", response.Code, reconciler.calls, response.Body.String())
	}
}

func TestMigrationStatusControllerRequiresServiceToken(t *testing.T) {
	reconciler := &controllerMigrationReconciler{}
	router, coreCalls := setupMigrationStatusController(t, reconciler, "migration.reconcile.as-service")
	request := httptest.NewRequest(http.MethodPost, "/internal/migrations/"+controllerMigrationID+"/reconcile", nil)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized || reconciler.calls != 0 || *coreCalls != 0 {
		t.Fatalf("status=%d calls=%d core=%d", response.Code, reconciler.calls, *coreCalls)
	}
}

func TestMigrationStatusControllerMapsErrors(t *testing.T) {
	tests := []struct {
		code   string
		status int
	}{
		{"invalid_request", http.StatusBadRequest},
		{"migration_not_found", http.StatusNotFound},
		{"migration_reconcile_failed", http.StatusInternalServerError},
	}
	for _, test := range tests {
		t.Run(test.code, func(t *testing.T) {
			reconciler := &controllerMigrationReconciler{err: &services.MigrationError{Code: test.code, Stage: "reconcile", Err: errors.New("upstream detail")}}
			router, _ := setupMigrationStatusController(t, reconciler, "migration.reconcile.as-service")
			request := httptest.NewRequest(http.MethodPost, "/internal/migrations/"+controllerMigrationID+"/reconcile", nil)
			request.Header.Set("Authorization", "Bearer service-token")
			response := httptest.NewRecorder()
			router.ServeHTTP(response, request)
			if response.Code != test.status || !strings.Contains(response.Body.String(), test.code) || strings.Contains(response.Body.String(), "upstream detail") {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
		})
	}
}
