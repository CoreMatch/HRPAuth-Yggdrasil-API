package clients

import (
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// stubHRPAuth simulates the two HRPAuth endpoints the startup credential
// resolution talks to. registerStatus/registerBody control POST
// /oauth/register; tokenStatus controls POST /oauth/token.
type stubHRPAuth struct {
	registerStatus int
	registerBody   string
	tokenStatus    int

	registerAuth string // captured Authorization header of the register call
	registerName string // captured client_name of the register call
	tokenAuth    string
	presenceAuth string
	presenceName string
}

func (s *stubHRPAuth) server() *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/oauth/register":
			s.registerAuth = r.Header.Get("Authorization")
			var body struct {
				ClientName string `json:"client_name"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			s.registerName = body.ClientName
			w.WriteHeader(s.registerStatus)
			_, _ = io.WriteString(w, s.registerBody)
		case "/oauth/token":
			s.tokenAuth = r.Header.Get("Authorization")
			w.WriteHeader(s.tokenStatus)
			_, _ = io.WriteString(w, `{"access_token":"t","token_type":"Bearer","expires_in":3600}`)
		case "/services/presence":
			s.presenceAuth = r.Header.Get("Authorization")
			var body PresenceRequest
			_ = json.NewDecoder(r.Body).Decode(&body)
			s.presenceName = body.Name
			w.WriteHeader(http.StatusOK)
			_, _ = io.WriteString(w, `{"success":true}`)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
}

func basicHeader(id, secret string) string {
	return "Basic " + base64.StdEncoding.EncodeToString([]byte(id+":"+secret))
}

func TestResolveServiceCredentialsExchangeSuccess(t *testing.T) {
	stub := &stubHRPAuth{
		registerStatus: http.StatusCreated,
		registerBody: `{"success":true,"data":{` +
			`"client_id":"HRPAuth-Yggdrasil-API","client_secret":"low-secret",` +
			`"client_id_issued_at":1700000000,"client_secret_expires_at":0,"scope":""}}`,
	}
	srv := stub.server()
	defer srv.Close()

	res, err := ResolveServiceCredentials(srv.URL, "HRPAuth-Yggdrasil-API", "hrpauth-internal-super", "super-secret", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !res.Exchanged {
		t.Fatal("expected Exchanged=true for super credentials")
	}
	if res.ClientID != "HRPAuth-Yggdrasil-API" || res.ClientSecret != "low-secret" {
		t.Errorf("resolved = %q/%q, want the exchanged per-service client", res.ClientID, res.ClientSecret)
	}
	if stub.registerAuth != basicHeader("hrpauth-internal-super", "super-secret") {
		t.Errorf("register Authorization = %q, want client_secret_basic of the configured pair", stub.registerAuth)
	}
	if stub.registerName != "HRPAuth-Yggdrasil-API" {
		t.Errorf("register client_name = %q, want the presence service name", stub.registerName)
	}
}

func TestResolveServiceCredentialsSuperRejected(t *testing.T) {
	stub := &stubHRPAuth{
		registerStatus: http.StatusBadRequest,
		registerBody:   `{"success":false,"code":"invalid_client_metadata","message":"service not in presence registry"}`,
	}
	srv := stub.server()
	defer srv.Close()

	_, err := ResolveServiceCredentials(srv.URL, "HRPAuth-Yggdrasil-API", "hrpauth-internal-super", "super-secret", nil)
	if err == nil {
		t.Fatal("expected an error when super credentials are rejected")
	}
	if !strings.Contains(err.Error(), "invalid_client_metadata") || !strings.Contains(err.Error(), "service not in presence registry") {
		t.Errorf("error = %q, want the rejection code and server message", err)
	}
}

func TestResolveServiceCredentialsIdempotentPath(t *testing.T) {
	stub := &stubHRPAuth{
		registerStatus: http.StatusUnauthorized,
		registerBody:   `{"success":false,"code":"oauth_invalid_client"}`,
		tokenStatus:    http.StatusOK,
	}
	srv := stub.server()
	defer srv.Close()

	res, err := ResolveServiceCredentials(srv.URL, "HRPAuth-Yggdrasil-API", "hrpauth-yggdrasil-api", "own-secret", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Exchanged {
		t.Fatal("non-super credentials must not be reported as exchanged")
	}
	if res.ClientID != "hrpauth-yggdrasil-api" || res.ClientSecret != "own-secret" {
		t.Errorf("resolved = %q/%q, want the configured pair unchanged", res.ClientID, res.ClientSecret)
	}
	if stub.tokenAuth != basicHeader("hrpauth-yggdrasil-api", "own-secret") {
		t.Errorf("token verification Authorization = %q, want client_secret_basic of the configured pair", stub.tokenAuth)
	}
}

func TestResolveServiceCredentialsInvalidCredentials(t *testing.T) {
	stub := &stubHRPAuth{
		registerStatus: http.StatusUnauthorized,
		registerBody:   `{"success":false,"code":"oauth_invalid_client"}`,
		tokenStatus:    http.StatusUnauthorized,
	}
	srv := stub.server()
	defer srv.Close()

	_, err := ResolveServiceCredentials(srv.URL, "HRPAuth-Yggdrasil-API", "ghost", "wrong", nil)
	if err == nil {
		t.Fatal("expected an error for credentials that are neither super nor a valid client")
	}
	if !strings.Contains(err.Error(), ErrCredentialsRejected.Error()) {
		t.Errorf("error = %q, want ErrCredentialsRejected", err)
	}
}

func TestResolveServiceCredentialsUnreachable(t *testing.T) {
	stub := &stubHRPAuth{registerStatus: http.StatusUnauthorized, tokenStatus: http.StatusOK}
	srv := stub.server()
	srv.Close() // simulate HRPAuth down at startup

	if _, err := ResolveServiceCredentials(srv.URL, "HRPAuth-Yggdrasil-API", "id", "secret", nil); err == nil {
		t.Fatal("expected an error when HRPAuth is unreachable (no lenient degradation)")
	}
}

func TestRegisterPresencePlainSendsNoToken(t *testing.T) {
	stub := &stubHRPAuth{}
	srv := stub.server()
	defer srv.Close()

	err := RegisterPresencePlain(srv.URL, PresenceRequest{Name: "HRPAuth-Yggdrasil-API", SecurityLevel: 0}, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if stub.presenceAuth != "" {
		t.Errorf("presence carried Authorization %q, want none before credential resolution", stub.presenceAuth)
	}
	if stub.presenceName != "HRPAuth-Yggdrasil-API" {
		t.Errorf("presence name = %q, want HRPAuth-Yggdrasil-API", stub.presenceName)
	}
}

func TestRegisterPresencePlainFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	if err := RegisterPresencePlain(srv.URL, PresenceRequest{Name: "HRPAuth-Yggdrasil-API"}, nil); err == nil {
		t.Fatal("expected an error for a failing presence endpoint")
	}
}
