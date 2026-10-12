package clients

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

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

func (c *CoreClient) RegisterPresence(req PresenceRequest) error {
	return c.doRequest("POST", "/services/presence", req, nil)
}

type RelayRule struct {
	Dest   string `json:"dest"`
	Source string `json:"source"`
}

type RelayRequest struct {
	Name   string      `json:"name"`
	Relays []RelayRule `json:"relays"`
}

func (c *CoreClient) RegisterRelay(req RelayRequest) error {
	return c.doRequest("POST", "/services/relay", req, nil)
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
