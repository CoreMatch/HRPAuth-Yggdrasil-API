package clients

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"strings"

	"github.com/lnb/HRPAuth-Yggdrasil-API/config"
	"github.com/lnb/HRPAuth-Yggdrasil-API/models"
	"github.com/lnb/HRPAuth-Yggdrasil-API/utils"
)

type CoreClient struct {
	BaseURL    string
	HTTPClient *http.Client
}

func NewCoreClient() *CoreClient {
	apiClient := utils.GetCoreAPIClient()
	httpClient, _ := apiClient.GetClient(context.Background())

	return &CoreClient{
		BaseURL:    config.AppConfig.CoreAPI.BaseURL,
		HTTPClient: httpClient,
	}
}

func (c *CoreClient) doRequest(method, path string, body interface{}, result interface{}) error {
	var bodyReader io.Reader
	if body != nil {
		jsonBody, err := json.Marshal(body)
		if err != nil {
			return err
		}
		bodyReader = bytes.NewBuffer(jsonBody)
	}

	req, err := http.NewRequest(method, c.BaseURL+path, bodyReader)
	if err != nil {
		return err
	}

	req.Header.Set("Content-Type", "application/json")

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("core api returned status %d", resp.StatusCode)
	}

	if result != nil {
		return json.NewDecoder(resp.Body).Decode(result)
	}

	return nil
}

// doRequestWithCode decodes the unified response envelope even on 4xx/5xx so
// callers can branch on the error `code`. It only returns an error for
// transport failures or undecodable bodies; HTTP-level rejection is carried
// by the decoded envelope (Success=false).
func (c *CoreClient) doRequestWithCode(method, path string, body interface{}, result interface{}) error {
	var bodyReader io.Reader
	if body != nil {
		jsonBody, err := json.Marshal(body)
		if err != nil {
			return err
		}
		bodyReader = bytes.NewBuffer(jsonBody)
	}

	req, err := http.NewRequest(method, c.BaseURL+path, bodyReader)
	if err != nil {
		return err
	}

	req.Header.Set("Content-Type", "application/json")

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(data, result); err != nil {
		return fmt.Errorf("core api returned status %d", resp.StatusCode)
	}
	return nil
}

func (c *CoreClient) VerifyCredentials(identifier, password string) (*models.User, error) {
	var user models.User
	body := map[string]string{
		"identifier": identifier,
		"password":   password,
	}
	err := c.doRequest("POST", "/internal/verify-credentials", body, &user)
	if err != nil {
		return nil, err
	}
	return &user, nil
}

func (c *CoreClient) VerifyToken(token string) (*models.User, error) {
	var resp struct {
		Success bool        `json:"success"`
		Data    models.User `json:"data"`
	}
	body := map[string]string{
		"token": token,
	}
	err := c.doRequest("POST", "/internal/verify-token", body, &resp)
	if err != nil {
		return nil, err
	}
	if !resp.Success {
		return nil, errors.New("token verification failed")
	}
	return &resp.Data, nil
}

// TokenContext is the resolved form of an OAuth2 access token on the core:
// either a user identity or a service identity with its scope set.
type TokenContext struct {
	User      *models.User // nil for service tokens
	IsService bool
	Scopes    []string
}

// ErrEmailAlreadyRegistered maps the core's 409 when the claimed email is
// already taken by another core user.
var ErrEmailAlreadyRegistered = errors.New("email already registered")

// ErrUsernameAlreadyBound maps the core's 409 when the claimed username
// belongs to another core user.
var ErrUsernameAlreadyBound = errors.New("username already bound")

// ResolveToken resolves any OAuth2 access token (user or service) via the
// core's /internal/verify-token.
func (c *CoreClient) ResolveToken(token string) (*TokenContext, error) {
	var resp struct {
		Success bool `json:"success"`
		Data    struct {
			UID       uint     `json:"uid"`
			UUID      string   `json:"uuid"`
			Email     string   `json:"email"`
			Username  string   `json:"username"`
			IsService bool     `json:"is_service"`
			Scopes    []string `json:"scopes"`
		} `json:"data"`
	}
	body := map[string]string{
		"token": token,
	}
	if err := c.doRequest("POST", "/internal/verify-token", body, &resp); err != nil {
		return nil, err
	}
	if !resp.Success {
		return nil, errors.New("token verification failed")
	}

	ctx := &TokenContext{
		IsService: resp.Data.IsService,
		Scopes:    resp.Data.Scopes,
	}
	if !ctx.IsService {
		if resp.Data.UUID == "" {
			return nil, errors.New("token verification failed")
		}
		ctx.User = &models.User{
			UID:      resp.Data.UID,
			UUID:     resp.Data.UUID,
			Email:    resp.Data.Email,
			Username: resp.Data.Username,
		}
	}
	return ctx, nil
}

// HasScope reports whether the scope set contains the required scope.
func (tc *TokenContext) HasScope(required string) bool {
	for _, scope := range tc.Scopes {
		if scope == required {
			return true
		}
	}
	return false
}

// ClaimUser asks the core to provision the user for a claimed proxy account.
// The core enforces email uniqueness and username idempotency; conflict
// statuses are mapped to typed errors.
func (c *CoreClient) ClaimUser(username, email, password string) (*models.User, error) {
	var resp struct {
		Success bool `json:"success"`
		Code    string
		Data    struct {
			UID  uint   `json:"uid"`
			UUID string `json:"uuid"`
		} `json:"data"`
	}
	body := map[string]string{
		"username": username,
		"email":    email,
		"password": password,
	}
	err := c.doRequestWithCode("POST", "/internal/claim-user", body, &resp)
	if err != nil {
		return nil, err
	}
	if !resp.Success {
		switch resp.Code {
		case "email_already_registered":
			return nil, ErrEmailAlreadyRegistered
		case "username_already_bound":
			return nil, ErrUsernameAlreadyBound
		}
		return nil, fmt.Errorf("claim rejected by core: %s", resp.Code)
	}
	return &models.User{UID: resp.Data.UID, UUID: resp.Data.UUID}, nil
}

type UserInfoResponse struct {
	Data models.User `json:"data"`
}

func (c *CoreClient) GetUserInfo(uuid string) (*models.User, error) {
	var resp UserInfoResponse
	err := c.doRequest("GET", "/services/users/"+uuid, nil, &resp)
	if err != nil {
		return nil, err
	}
	return &resp.Data, nil
}

type PresenceRequest struct {
	Name          string                 `json:"name"`
	TTLSeconds    int                    `json:"ttl_seconds,omitempty"`
	Scope         *PresenceScope         `json:"scope,omitempty"`
	SDKURL        string                 `json:"sdk_url,omitempty"`
	SecurityLevel int                    `json:"security_level"`
	RootMetadata  map[string]interface{} `json:"root_metadata,omitempty"`
}

type PresenceScope struct {
	Name          string   `json:"name"`
	FrontendAreas []string `json:"frontend_areas"`
}

type RelayRule struct {
	Dest   string `json:"dest"`
	Source string `json:"source"`
}

type RelayRequest struct {
	Name   string      `json:"name"`
	Relays []RelayRule `json:"relays"`
}

type CoreMetadata struct {
	Site struct {
		URL         string `json:"url"`
		FrontendURL string `json:"frontend_url"`
	} `json:"site"`
}

func (c *CoreClient) GetMetadata() (*CoreMetadata, error) {
	var meta CoreMetadata
	// HRPAuth Core's root endpoint / provides metadata
	err := c.doRequest("GET", "/", nil, &meta)
	if err != nil {
		return nil, err
	}
	return &meta, nil
}

// ErrSDKPackageConflict is returned when a package with the same name already exists (409).
var ErrSDKPackageConflict = errors.New("sdk package conflict")

func (c *CoreClient) UploadSDKPackage(data []byte, filename string) error {
	url := c.BaseURL + "/services/sdk-packages"

	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, err := mw.CreateFormFile("package", filename)
	if err != nil {
		return err
	}
	if _, err := fw.Write(data); err != nil {
		return err
	}
	if err := mw.Close(); err != nil {
		return err
	}

	req, err := http.NewRequest(http.MethodPost, url, &buf)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusConflict {
		return ErrSDKPackageConflict
	}

	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return nil
	}

	body, _ := io.ReadAll(resp.Body)
	return fmt.Errorf("upstream returned status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
}

func (c *CoreClient) DeleteSDKPackage(name string) error {
	url := c.BaseURL + "/services/sdk-packages/" + name
	req, err := http.NewRequest(http.MethodDelete, url, nil)
	if err != nil {
		return err
	}

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNotFound {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("upstream returned status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return nil
}
