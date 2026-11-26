package clients

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestClaimMigrationUserProvidedEmail(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/internal/claim-user" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		var body migrationClaimRequest
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		if body.Username != "Player" || body.MigrationID != "123e4567-e89b-12d3-a456-426614174000" || body.Password != "secret-password" || body.EmailMode != "provided" || body.Email == nil || *body.Email != "Player@example.com" {
			t.Errorf("unexpected body: %+v", body)
		}
		_, _ = w.Write([]byte(`{"success":true,"data":{"uid":7,"uuid":"0123456789abcdef0123456789abcdef","email":"player@example.com","created":true}}`))
	}))
	defer server.Close()
	client := &CoreClient{BaseURL: server.URL, HTTPClient: server.Client()}
	email := "Player@example.com"
	claim, err := client.ClaimMigrationUser(context.Background(), "Player", "123e4567-e89b-12d3-a456-426614174000", "secret-password", &email)
	if err != nil {
		t.Fatal(err)
	}
	if claim.UID != 7 || claim.UUID != "0123456789abcdef0123456789abcdef" || claim.Email != "player@example.com" || !claim.Created {
		t.Fatalf("unexpected claim: %+v", claim)
	}
}

func TestClaimMigrationUserPlaceholderEmail(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		if _, exists := body["email"]; exists || body["email_mode"] != "placeholder" {
			t.Errorf("placeholder request contains email or wrong mode: %+v", body)
		}
		_, _ = w.Write([]byte(`{"success":true,"data":{"uid":8,"uuid":"1123456789abcdef0123456789abcdef","email":"migration-123e4567-e89b-12d3-a456-426614174000@placeholder.invalid","created":false}}`))
	}))
	defer server.Close()
	client := &CoreClient{BaseURL: server.URL, HTTPClient: server.Client()}
	claim, err := client.ClaimMigrationUser(context.Background(), "Player", "123e4567-e89b-12d3-a456-426614174000", "secret-password", nil)
	if err != nil {
		t.Fatal(err)
	}
	if claim.Created || claim.UID != 8 {
		t.Fatalf("unexpected claim: %+v", claim)
	}
}

func TestClaimMigrationUserErrors(t *testing.T) {
	tests := []struct {
		name   string
		status int
		body   string
		want   error
	}{
		{"migration conflict", http.StatusConflict, `{"success":false,"code":"migration_conflict"}`, ErrMigrationConflict},
		{"rolled back", http.StatusConflict, `{"success":false,"code":"migration_invalid_state"}`, ErrMigrationInvalidState},
		{"username", http.StatusConflict, `{"success":false,"code":"username_already_bound"}`, ErrUsernameAlreadyBound},
		{"email", http.StatusConflict, `{"success":false,"code":"email_already_registered"}`, ErrEmailAlreadyRegistered},
		{"upstream", http.StatusInternalServerError, `{"success":false,"code":"internal_error"}`, nil},
		{"missing created", http.StatusOK, `{"success":true,"data":{"uid":1,"uuid":"0123456789abcdef0123456789abcdef","email":"migration-123e4567-e89b-12d3-a456-426614174000@placeholder.invalid"}}`, ErrInvalidCoreResponse},
		{"wrong placeholder", http.StatusOK, `{"success":true,"data":{"uid":1,"uuid":"0123456789abcdef0123456789abcdef","email":"other@placeholder.invalid","created":true}}`, ErrInvalidCoreResponse},
		{"malformed", http.StatusOK, `not-json`, ErrInvalidCoreResponse},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(test.status)
				_, _ = w.Write([]byte(test.body))
			}))
			defer server.Close()
			client := &CoreClient{BaseURL: server.URL, HTTPClient: server.Client()}
			_, err := client.ClaimMigrationUser(context.Background(), "Player", "123e4567-e89b-12d3-a456-426614174000", "secret-password", nil)
			if test.want != nil && !errors.Is(err, test.want) {
				t.Fatalf("error = %v, want %v", err, test.want)
			}
			if test.want == nil {
				var apiError *CoreAPIError
				if !errors.As(err, &apiError) || apiError.StatusCode != test.status {
					t.Fatalf("error = %v, want CoreAPIError status %d", err, test.status)
				}
			}
		})
	}
}

func TestClaimMigrationUserHonorsContext(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	defer server.Close()
	client := &CoreClient{BaseURL: server.URL, HTTPClient: server.Client()}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := client.ClaimMigrationUser(ctx, "Player", "123e4567-e89b-12d3-a456-426614174000", "secret-password", nil)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context cancellation", err)
	}
}

func TestVerifyCredentialsContextReturnsCoreIdentity(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/internal/verify-credentials" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"uid":9,"uuid":"0123456789abcdef0123456789abcdef","email":"player@example.com","username":"Player"}`))
	}))
	defer server.Close()
	client := &CoreClient{BaseURL: server.URL, HTTPClient: server.Client()}
	user, err := client.VerifyCredentialsContext(context.Background(), "Player", "secret-password")
	if err != nil {
		t.Fatal(err)
	}
	if user.UID != 9 || user.UUID != "0123456789abcdef0123456789abcdef" || user.Username != "Player" {
		t.Fatalf("unexpected user: %+v", user)
	}
}

func TestVerifyCredentialsContextHonorsCancellation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	defer server.Close()
	client := &CoreClient{BaseURL: server.URL, HTTPClient: server.Client()}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := client.VerifyCredentialsContext(ctx, "Player", "secret-password"); !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context cancellation", err)
	}
}

func TestResolveTokenContextServiceScopes(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.Header.Get("Content-Type"), "application/json") {
			t.Error("missing JSON content type")
		}
		_, _ = w.Write([]byte(`{"success":true,"data":{"is_service":true,"scopes":["migration.commit.as-service"]}}`))
	}))
	defer server.Close()
	client := &CoreClient{BaseURL: server.URL, HTTPClient: server.Client()}
	token, err := client.ResolveTokenContext(context.Background(), "service-token")
	if err != nil {
		t.Fatal(err)
	}
	if !token.IsService || !token.HasScope("migration.commit.as-service") || token.HasScope("migration.rollback.as-service") {
		t.Fatalf("unexpected token context: %+v", token)
	}
}
