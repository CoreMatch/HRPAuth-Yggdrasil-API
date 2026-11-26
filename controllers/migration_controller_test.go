package controllers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/lnb/HRPAuth-Yggdrasil-API/clients"
	"github.com/lnb/HRPAuth-Yggdrasil-API/services"
)

const controllerMigrationID = "123e4567-e89b-12d3-a456-426614174000"
const controllerProfileID = "0123456789abcdef0123456789abcdef"

type controllerMigrationCommitter struct {
	request services.MigrationCommitRequest
	result  services.MigrationCommitResult
	err     error
	calls   int
}

func (c *controllerMigrationCommitter) Commit(ctx context.Context, request services.MigrationCommitRequest) (services.MigrationCommitResult, error) {
	c.calls++
	c.request = request
	return c.result, c.err
}

func setupMigrationController(t *testing.T, committer *controllerMigrationCommitter) (*gin.Engine, *int) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	coreCalls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		coreCalls++
		_ = json.NewEncoder(w).Encode(map[string]any{
			"success": true,
			"data": map[string]any{
				"is_service": true,
				"scopes":     []string{"migration.commit.as-service"},
			},
		})
	}))
	t.Cleanup(server.Close)
	core := &clients.CoreClient{BaseURL: server.URL, HTTPClient: server.Client()}
	router := gin.New()
	router.Use(RequestIDMiddleware())
	router.POST("/internal/migrations/commit", NewMigrationController(core, committer).Commit)
	return router, &coreCalls
}

func controllerMetadata() services.MigrationMetadata {
	return services.MigrationMetadata{
		MigrationID: controllerMigrationID, Identifier: "Player", Password: "secret-password",
		Username: "Player", ProfileUUID: controllerProfileID, ProfileName: "Player", ProfileModel: "default",
		LegacyUserProperties: map[string]string{"legacy_rank": "vip"},
	}
}

func controllerMultipart(t *testing.T, metadata services.MigrationMetadata, extra ...map[string]string) ([]byte, string) {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	encoded, err := json.Marshal(metadata)
	if err != nil {
		t.Fatal(err)
	}
	part, err := writer.CreatePart(textproto.MIMEHeader{
		"Content-Disposition": []string{`form-data; name="metadata"`},
		"Content-Type":        []string{"application/json"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write(encoded); err != nil {
		t.Fatal(err)
	}
	for _, fields := range extra {
		for name, value := range fields {
			if err := writer.WriteField(name, value); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return body.Bytes(), writer.FormDataContentType()
}

func performMigrationRequest(router *gin.Engine, body []byte, contentType, idempotencyKey string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(http.MethodPost, "/internal/migrations/commit", bytes.NewReader(body))
	request.Header.Set("Content-Type", contentType)
	request.Header.Set("Authorization", "Bearer service-token")
	if idempotencyKey != "" {
		request.Header.Set("Idempotency-Key", idempotencyKey)
	}
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	return response
}

func TestMigrationControllerCommit(t *testing.T) {
	committer := &controllerMigrationCommitter{result: services.MigrationCommitResult{
		MigrationID: controllerMigrationID, Status: "committed", CoreUID: "7", AccountID: 1, ProfileID: controllerProfileID,
		CreatedCoreUser: true,
		Session: services.MigrationSession{
			AccessToken: "access-token", ClientToken: "client-token",
			SelectedProfile: services.MigrationProfile{ID: controllerProfileID, Name: "Player"},
		},
		User:        services.MigrationUser{ID: controllerProfileID},
		Assets:      services.MigrationAssetResults{Skin: services.MigrationAssetResult{Status: "absent"}, Cape: services.MigrationAssetResult{Status: "absent"}},
		CommittedAt: time.Now().UTC(),
	}}
	router, coreCalls := setupMigrationController(t, committer)
	body, contentType := controllerMultipart(t, controllerMetadata())
	response := performMigrationRequest(router, body, contentType, controllerMigrationID)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", response.Code, response.Body.String())
	}
	if committer.calls != 1 || *coreCalls != 1 || committer.request.Password != "secret-password" || committer.request.Metadata.ProfileUUID != controllerProfileID {
		t.Fatalf("unexpected commit: calls=%d core=%d request=%+v", committer.calls, *coreCalls, committer.request)
	}
	if strings.Contains(response.Body.String(), "secret-password") {
		t.Fatal("response leaked commit password")
	}
	var envelope struct {
		Success bool                           `json:"success"`
		Data    services.MigrationCommitResult `json:"data"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil || !envelope.Success || envelope.Data.MigrationID != controllerMigrationID {
		t.Fatalf("unexpected response: %s err=%v", response.Body.String(), err)
	}
}

func TestMigrationControllerRejectsInvalidMultipart(t *testing.T) {
	tests := []struct {
		name  string
		key   string
		extra []map[string]string
	}{
		{name: "missing idempotency", key: ""},
		{name: "wrong idempotency", key: "123e4567-e89b-12d3-a456-426614174001"},
		{name: "duplicate metadata", key: controllerMigrationID, extra: []map[string]string{{"metadata": `{}`}}},
		{name: "unknown field", key: controllerMigrationID, extra: []map[string]string{{"unexpected": "value"}}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			committer := &controllerMigrationCommitter{}
			router, _ := setupMigrationController(t, committer)
			body, contentType := controllerMultipart(t, controllerMetadata(), test.extra...)
			response := performMigrationRequest(router, body, contentType, test.key)
			if response.Code != http.StatusBadRequest || committer.calls != 0 {
				t.Fatalf("status=%d calls=%d body=%s", response.Code, committer.calls, response.Body.String())
			}
		})
	}
}

func TestMigrationControllerRequiresServiceToken(t *testing.T) {
	committer := &controllerMigrationCommitter{}
	router, coreCalls := setupMigrationController(t, committer)
	body, contentType := controllerMultipart(t, controllerMetadata())
	request := httptest.NewRequest(http.MethodPost, "/internal/migrations/commit", bytes.NewReader(body))
	request.Header.Set("Content-Type", contentType)
	request.Header.Set("Idempotency-Key", controllerMigrationID)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized || committer.calls != 0 || *coreCalls != 0 {
		t.Fatalf("status=%d calls=%d core=%d", response.Code, committer.calls, *coreCalls)
	}
}

func TestMigrationControllerMapsCommitErrors(t *testing.T) {
	tests := []struct {
		code   string
		status int
	}{
		{"invalid_texture_file", http.StatusBadRequest},
		{"migration_conflict", http.StatusConflict},
		{"migration_target_not_empty", http.StatusConflict},
		{"migration_commit_failed", http.StatusInternalServerError},
	}
	for _, test := range tests {
		t.Run(test.code, func(t *testing.T) {
			committer := &controllerMigrationCommitter{err: &services.MigrationError{Code: test.code, Stage: "verify", Err: errors.New("upstream detail")}}
			router, _ := setupMigrationController(t, committer)
			body, contentType := controllerMultipart(t, controllerMetadata())
			response := performMigrationRequest(router, body, contentType, controllerMigrationID)
			if response.Code != test.status || !strings.Contains(response.Body.String(), test.code) || strings.Contains(response.Body.String(), "upstream detail") {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
		})
	}
}

func TestMigrationControllerRejectsOversizeBody(t *testing.T) {
	committer := &controllerMigrationCommitter{}
	router, _ := setupMigrationController(t, committer)
	body, contentType := controllerMultipart(t, controllerMetadata(), map[string]string{"padding": strings.Repeat("x", int(migrationCommitMaxBodyBytes))})
	response := performMigrationRequest(router, body, contentType, controllerMigrationID)
	if response.Code != http.StatusRequestEntityTooLarge || committer.calls != 0 {
		t.Fatalf("status=%d calls=%d body=%s", response.Code, committer.calls, response.Body.String())
	}
}
