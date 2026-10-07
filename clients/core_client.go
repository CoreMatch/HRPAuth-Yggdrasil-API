package clients

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/lnb/HRPAuth-Yggdrasil-API/config"
	"github.com/lnb/HRPAuth-Yggdrasil-API/models"
)

type CoreClient struct {
	BaseURL     string
	InternalKey string
	HTTPClient  *http.Client
}

func NewCoreClient() *CoreClient {
	return &CoreClient{
		BaseURL:     config.AppConfig.CoreAPI.BaseURL,
		InternalKey: config.AppConfig.CoreAPI.InternalKey,
		HTTPClient: &http.Client{
			Timeout: 10 * time.Second,
		},
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
	req.Header.Set("X-Internal-Key", c.InternalKey)

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

type PresenceRequest struct {
	Name          string         `json:"name"`
	TTLSeconds    int            `json:"ttl_seconds,omitempty"`
	Scope         *PresenceScope `json:"scope,omitempty"`
	SDKURL        string         `json:"sdk_url,omitempty"`
	SecurityLevel int            `json:"security_level"`
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
		URL string `json:"url"`
	} `json:"site"`
	Yggdrasil struct {
		Meta struct {
			Links struct {
				Homepage string `json:"homepage"`
			} `json:"links"`
		} `json:"meta"`
	} `json:"yggdrasil"`
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
