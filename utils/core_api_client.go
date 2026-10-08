package utils

import (
	"context"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/lnb/HRPAuth-Yggdrasil-API/config"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/clientcredentials"
)

type CoreAPIClient struct {
	config *clientcredentials.Config
	token  *oauth2.Token
	mu     sync.Mutex
}

var (
	coreAPIClientInstance *CoreAPIClient
	once                  sync.Once
)

func GetCoreAPIClient() *CoreAPIClient {
	once.Do(func() {
		cfg := config.AppConfig.CoreAPI
		if cfg.ClientID == "" || cfg.ClientSecret == "" {
			log.Println("CoreAPI client_id or client_secret is not configured. Service-to-service calls will be disabled.")
			return
		}

		coreAPIClientInstance = &CoreAPIClient{
			config: &clientcredentials.Config{
				ClientID:     cfg.ClientID,
				ClientSecret: cfg.ClientSecret,
				TokenURL:     cfg.BaseURL + "/oauth/token",
			},
		}
	})
	return coreAPIClientInstance
}

func (c *CoreAPIClient) GetToken(ctx context.Context) (*oauth2.Token, error) {
	if c == nil {
		return nil, nil // Client not configured
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	// If token is nil or expires in less than 60 seconds, fetch a new one.
	if c.token == nil || c.token.Expiry.Before(time.Now().Add(60*time.Second)) {
		token, err := c.config.Token(ctx)
		if err != nil {
			return nil, err
		}
		c.token = token
	}

	return c.token, nil
}

func (c *CoreAPIClient) GetClient(ctx context.Context) (*http.Client, error) {
	if c == nil {
		return http.DefaultClient, nil // Return default client if not configured
	}

	return c.config.Client(ctx), nil
}
