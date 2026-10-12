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
