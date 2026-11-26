package clients

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/lnb/HRPAuth-Yggdrasil-API/models"
)

var ErrCoreUserNotFound = errors.New("core user not found")

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
