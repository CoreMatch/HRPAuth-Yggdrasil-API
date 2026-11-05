package clients

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// HTTPDoer is the minimal interface the self-registration client needs
// from net/http; tests inject an *http.Client backed by httptest.
type HTTPDoer interface {
	Do(*http.Request) (*http.Response, error)
}

// ErrCredentialsRejected is returned when the configured credentials are
// neither the HRPAuth built-in super client nor a valid service client.
// Startup must abort with this error; there is deliberately no fallback to
// running business traffic with unverified or super credentials.
var ErrCredentialsRejected = errors.New("clients: credentials rejected")

// Resolution is the outcome of the startup credential resolution.
type Resolution struct {
	ClientID     string
	ClientSecret string
	// Exchanged reports whether the configured super credentials were
	// exchanged for this service's own low-privilege client through
	// POST /oauth/register. When false the configured credentials were
	// confirmed to be an ordinary service client and are reused as-is.
	Exchanged bool
}

// registrationEnvelope mirrors the HRPAuth POST /oauth/register response:
// the unified envelope carries the RFC 7591 client metadata both in
// data and flattened at the top level.
type registrationEnvelope struct {
	ClientID              string                `json:"client_id"`
	ClientSecret          string                `json:"client_secret"`
	ClientIDIssuedAt      int64                 `json:"client_id_issued_at"`
	ClientSecretExpiresAt int64                 `json:"client_secret_expires_at"`
	Scope                 string                `json:"scope"`
	Code                  string                `json:"code"`
	Message               string                `json:"message"`
	Data                  *registrationEnvelope `json:"data"`
}

// ResolveServiceCredentials classifies the configured HRPAuth credentials
// and, when they turn out to be the built-in super client, exchanges them
// for this service's own low-privilege client via POST /oauth/register
// (RFC 7591 protected registration; the super client authenticates with
// client_secret_basic).
//
// Classification — the register endpoint only authenticates the super
// client, so its answer is authoritative:
//
//   - 201: super credentials; the exchanged per-service client is
//     returned (Exchanged=true). The response secret is stable across
//     restarts (HRPAuth get-or-create), so it never needs persisting by
//     this service.
//   - 400 invalid_client_metadata: super credentials, but the
//     registration was rejected (service name not presence-registered,
//     scope outside the allowlist, malformed body). Fatal — the exchange
//     cannot be retried with different metadata by this service.
//   - 401: not the super client. The credentials are then verified with a
//     plain client_credentials token request; success means the service
//     already holds its own low-privilege client (idempotent path:
//     Exchanged=false, nothing may be written back).
//
// Every failure is returned as an error. Callers must abort startup
// rather than fall back to running business traffic with the configured
// credentials: super credentials must never reach business requests, and
// unverified credentials must not be trusted.
//
// serviceName must be the name this service registers under via
// /services/presence — HRPAuth rejects registration for unknown names.
// The caller is responsible for completing the presence handshake before
// invoking this function.
func ResolveServiceCredentials(rootURL, serviceName, clientID, clientSecret string, doer HTTPDoer) (*Resolution, error) {
	httpClient := plainHTTPClient(doer)
	rootURL = strings.TrimRight(rootURL, "/")

	registerURL, err := url.JoinPath(rootURL, "/oauth/register")
	if err != nil {
		return nil, err
	}
	body, err := json.Marshal(map[string]string{"client_name": serviceName})
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequest(http.MethodPost, registerURL, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Basic "+basicAuth(clientID, clientSecret))

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("clients: credential exchange request failed: %w", err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("clients: credential exchange response read failed: %w", err)
	}

	switch resp.StatusCode {
	case http.StatusCreated:
		var env registrationEnvelope
		if err := json.Unmarshal(raw, &env); err != nil {
			return nil, fmt.Errorf("clients: credential exchange response undecodable: %w", err)
		}
		meta := &env
		if env.Data != nil && env.Data.ClientID != "" {
			meta = env.Data
		}
		if meta.ClientID == "" || meta.ClientSecret == "" {
			return nil, errors.New("clients: credential exchange returned no client credentials")
		}
		return &Resolution{ClientID: meta.ClientID, ClientSecret: meta.ClientSecret, Exchanged: true}, nil

	case http.StatusBadRequest:
		// Authentication passed (only the super client gets this far),
		// but the registration metadata was rejected.
		var env registrationEnvelope
		_ = json.Unmarshal(raw, &env)
		detail := env.Message
		if detail == "" {
			detail = string(raw)
		}
		return nil, fmt.Errorf("clients: super credentials detected but self-registration was rejected (%s): %s", env.Code, detail)

	case http.StatusUnauthorized:
		// Not the super client: verify that the credentials are a valid
		// ordinary service client before trusting them.
		if err := verifyServiceCredentials(rootURL, clientID, clientSecret, httpClient); err != nil {
			return nil, err
		}
		return &Resolution{ClientID: clientID, ClientSecret: clientSecret, Exchanged: false}, nil

	default:
		_, _ = io.Copy(io.Discard, resp.Body)
		return nil, fmt.Errorf("clients: credential exchange answered status %d", resp.StatusCode)
	}
}

// verifyServiceCredentials performs a plain client_credentials token
// request to prove the configured pair is a usable service client.
func verifyServiceCredentials(rootURL, clientID, clientSecret string, httpClient *http.Client) error {
	tokenURL, err := url.JoinPath(rootURL, "/oauth/token")
	if err != nil {
		return err
	}
	req, err := http.NewRequest(http.MethodPost, tokenURL,
		strings.NewReader("grant_type=client_credentials"))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Basic "+basicAuth(clientID, clientSecret))

	resp, err := httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("%w: token verification request failed: %v", ErrCredentialsRejected, err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%w: not the super client and no valid service client either (token endpoint answered %d)",
			ErrCredentialsRejected, resp.StatusCode)
	}
	return nil
}

// RegisterPresencePlain performs the presence (bonjour) handshake without
// attaching any OAuth2 token. The endpoint itself requires no
// authentication, and the startup credential resolution must not put a
// super-derived token on the wire for anything other than the exchange
// call itself.
func RegisterPresencePlain(rootURL string, req PresenceRequest, doer HTTPDoer) error {
	httpClient := plainHTTPClient(doer)
	u, err := url.JoinPath(strings.TrimRight(rootURL, "/"), "/services/presence")
	if err != nil {
		return err
	}
	var buf bytes.Buffer
	if err := json.NewEncoder(&buf).Encode(req); err != nil {
		return err
	}
	httpReq, err := http.NewRequest(http.MethodPost, u, &buf)
	if err != nil {
		return err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "application/json")

	resp, err := httpClient.Do(httpReq)
	if err != nil {
		return fmt.Errorf("clients: presence registration request failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, resp.Body)
		return fmt.Errorf("clients: presence registration returned status %d", resp.StatusCode)
	}
	return nil
}

func basicAuth(clientID, clientSecret string) string {
	return base64.StdEncoding.EncodeToString([]byte(clientID + ":" + clientSecret))
}

func plainHTTPClient(doer HTTPDoer) *http.Client {
	if doer != nil {
		if httpClient, ok := doer.(*http.Client); ok {
			return httpClient
		}
	}
	return http.DefaultClient
}
