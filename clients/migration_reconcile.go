package clients

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/lnb/HRPAuth-Yggdrasil-API/models"
)

var (
	ErrCoreUserNotFound            = errors.New("core user not found")
	ErrMigrationNotFound           = errors.New("migration not found")
	ErrMigrationRollbackNotAllowed = errors.New("migration rollback not allowed")
	coreMigrationIDPattern         = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
)

type CoreMigrationUser struct {
	UID      uint   `json:"uid"`
	UUID     string `json:"uuid"`
	Email    string `json:"email"`
	Username string `json:"username"`
	Present  bool   `json:"present"`
}

type CoreMigrationStatus struct {
	MigrationID string             `json:"migration_id"`
	Status      string             `json:"status"`
	Ownership   string             `json:"ownership"`
	User        *CoreMigrationUser `json:"user,omitempty"`
	UpdatedAt   time.Time          `json:"updated_at"`
}

type CoreMigrationRollback struct {
	MigrationID string `json:"migration_id"`
	Status      string `json:"status"`
	UID         uint   `json:"uid,omitempty"`
}

func (c *CoreClient) InspectMigrationUser(ctx context.Context, uuid string) (*models.User, error) {
	if !validCoreUUID(uuid) {
		return nil, ErrInvalidCoreResponse
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+"/services/users/"+uuid, nil)
	if err != nil {
		return nil, err
	}
	response, err := c.HTTPClient.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	payload, err := io.ReadAll(io.LimitReader(response.Body, (1<<20)+1))
	if err != nil {
		return nil, err
	}
	if len(payload) > 1<<20 {
		return nil, ErrInvalidCoreResponse
	}
	var envelope struct {
		Success bool   `json:"success"`
		Code    string `json:"code"`
		Data    struct {
			UID      uint   `json:"uid"`
			UUID     string `json:"uuid"`
			Email    string `json:"email"`
			Username string `json:"username"`
		} `json:"data"`
	}
	if err := json.Unmarshal(payload, &envelope); err != nil {
		return nil, ErrInvalidCoreResponse
	}
	if response.StatusCode == http.StatusNotFound && !envelope.Success && envelope.Code == "user_not_found" {
		return nil, ErrCoreUserNotFound
	}
	if response.StatusCode != http.StatusOK || !envelope.Success {
		code := envelope.Code
		if code == "" {
			code = "migration_reconcile_failed"
		}
		return nil, &CoreAPIError{StatusCode: response.StatusCode, Code: code}
	}
	if envelope.Data.UID == 0 || !validCoreUUID(envelope.Data.UUID) ||
		!strings.EqualFold(envelope.Data.UUID, uuid) || envelope.Data.Email == "" || envelope.Data.Username == "" {
		return nil, ErrInvalidCoreResponse
	}
	return &models.User{
		UID: envelope.Data.UID, UUID: envelope.Data.UUID,
		Email: envelope.Data.Email, Username: envelope.Data.Username,
	}, nil
}

func (c *CoreClient) InspectMigration(ctx context.Context, migrationID string) (*CoreMigrationStatus, error) {
	if !coreMigrationIDPattern.MatchString(migrationID) {
		return nil, ErrInvalidCoreResponse
	}
	var envelope struct {
		Success bool                `json:"success"`
		Code    string              `json:"code"`
		Data    CoreMigrationStatus `json:"data"`
	}
	statusCode, err := c.migrationRequestContext(ctx, http.MethodGet, "/internal/migrations/"+migrationID, &envelope)
	if err != nil {
		return nil, err
	}
	if statusCode == http.StatusNotFound && envelope.Code == "migration_not_found" {
		return nil, ErrMigrationNotFound
	}
	if statusCode != http.StatusOK || !envelope.Success {
		return nil, &CoreAPIError{StatusCode: statusCode, Code: migrationErrorCode(envelope.Code)}
	}
	status := envelope.Data
	if status.MigrationID != migrationID || status.UpdatedAt.IsZero() ||
		(status.Status != "not_created" && status.Status != "created" && status.Status != "rolled_back") ||
		(status.Ownership != "none" && status.Ownership != "created_by_migration" && status.Ownership != "preexisting") {
		return nil, ErrInvalidCoreResponse
	}
	if status.User != nil {
		if status.User.UID == 0 || !validCoreUUID(status.User.UUID) || status.User.Email == "" || status.User.Username == "" {
			return nil, ErrInvalidCoreResponse
		}
	}
	return &status, nil
}

func (c *CoreClient) RollbackMigration(ctx context.Context, migrationID string) (*CoreMigrationRollback, error) {
	if !coreMigrationIDPattern.MatchString(migrationID) {
		return nil, ErrInvalidCoreResponse
	}
	var envelope struct {
		Success bool                  `json:"success"`
		Code    string                `json:"code"`
		Data    CoreMigrationRollback `json:"data"`
	}
	statusCode, err := c.migrationRequestContext(ctx, http.MethodPost, "/internal/migrations/"+migrationID+"/rollback", &envelope)
	if err != nil {
		return nil, err
	}
	if statusCode == http.StatusNotFound && envelope.Code == "migration_not_found" {
		return nil, ErrMigrationNotFound
	}
	if statusCode == http.StatusConflict {
		switch envelope.Code {
		case "migration_rollback_not_allowed":
			return nil, ErrMigrationRollbackNotAllowed
		case "migration_invalid_state":
			return nil, ErrMigrationInvalidState
		}
	}
	if statusCode != http.StatusOK || !envelope.Success {
		return nil, &CoreAPIError{StatusCode: statusCode, Code: migrationErrorCode(envelope.Code)}
	}
	if envelope.Data.MigrationID != migrationID || envelope.Data.Status != "rolled_back" {
		return nil, ErrInvalidCoreResponse
	}
	return &envelope.Data, nil
}

func migrationErrorCode(code string) string {
	if code == "" {
		return "migration_reconcile_failed"
	}
	return code
}

func (c *CoreClient) migrationRequestContext(ctx context.Context, method, path string, result any) (int, error) {
	request, err := http.NewRequestWithContext(ctx, method, c.BaseURL+path, nil)
	if err != nil {
		return 0, err
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := c.HTTPClient.Do(request)
	if err != nil {
		return 0, err
	}
	defer response.Body.Close()
	payload, err := io.ReadAll(io.LimitReader(response.Body, (1<<20)+1))
	if err != nil {
		return 0, err
	}
	if len(payload) > 1<<20 {
		return response.StatusCode, ErrInvalidCoreResponse
	}
	if err := json.Unmarshal(payload, result); err != nil {
		return response.StatusCode, ErrInvalidCoreResponse
	}
	return response.StatusCode, nil
}
