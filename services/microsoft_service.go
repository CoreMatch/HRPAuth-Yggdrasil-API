package services

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/lnb/HRPAuth-Yggdrasil-API/config"
)

// Microsoft-login mapping (HA-Contract hrpauth-yggdrasil-business.yaml
// /authserver/microsoft, /player/microsoft-bind, /player/microsoft-unbind).
//
// The server never trusts client-supplied uuid/name claims: the Minecraft
// Services access token is presented to the Minecraft Services profile
// endpoint and Mojang's answer is authoritative. The base URL is swappable
// (yggdrasil.microsoft.minecraft_services_base) so the whole chain can run
// against a local mock until the Azure application passes Mojang's allowlist
// review; only configuration changes then, never the code path.

// Typed re-verification outcomes. Each maps onto a distinct contract error so
// operators and clients can tell why a genuine login did not complete.
var (
	// ErrMCVerificationFailed: the token was rejected (401/403) — invalid or
	// expired MC access token.
	ErrMCVerificationFailed = errors.New("minecraft verification failed")
	// ErrMCProfileMissing: license present but no game profile yet (profile
	// endpoint 404 while entitlements show ownership).
	ErrMCProfileMissing = errors.New("minecraft profile missing")
	// ErrMCLicenseNotFound: no ownership and no profile (profile endpoint 404
	// without entitlements).
	ErrMCLicenseNotFound = errors.New("minecraft license not found")
	// ErrMCUpstreamUnavailable: network failure, timeout or 5xx during
	// re-verification; retryable.
	ErrMCUpstreamUnavailable = errors.New("minecraft services unavailable")
)

// MCProfile is the genuine identity resolved from a Minecraft Services
// access token. ID is the mojang_uuid binding key (32-hex, lowercased).
type MCProfile struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// MicrosoftService re-verifies Minecraft Services access tokens.
type MicrosoftService struct {
	httpClient *http.Client
	base       string
}

func NewMicrosoftService() *MicrosoftService {
	base := config.AppConfig.Yggdrasil.Microsoft.MinecraftServicesBase
	if base == "" {
		base = config.DefaultMinecraftServicesBase
	}
	timeout := config.AppConfig.Yggdrasil.Microsoft.TimeoutSeconds
	if timeout <= 0 {
		timeout = 15
	}
	return &MicrosoftService{
		httpClient: &http.Client{Timeout: time.Duration(timeout) * time.Second},
		base:       strings.TrimRight(base, "/"),
	}
}

// mcEntitlements mirrors the entitlements response subset we consume.
type mcEntitlements struct {
	Items []struct {
		Name string `json:"name"`
	} `json:"items"`
}

// VerifyMCToken resolves the genuine identity behind a Minecraft Services
// access token. It returns exactly one of the typed errors above on failure;
// the token is never logged, stored or returned.
func (ms *MicrosoftService) VerifyMCToken(ctx context.Context, mcAccessToken string) (*MCProfile, error) {
	mcAccessToken = strings.TrimSpace(mcAccessToken)
	if mcAccessToken == "" {
		return nil, ErrMCVerificationFailed
	}

	status, body, err := ms.get(ctx, "/minecraft/profile", mcAccessToken)
	if err != nil {
		return nil, ErrMCUpstreamUnavailable
	}

	switch {
	case status == http.StatusOK:
		var profile MCProfile
		if err := json.Unmarshal(body, &profile); err != nil || profile.ID == "" || profile.Name == "" {
			// An undecodable 200 is an upstream contract break; treat it as
			// unavailable so the caller can retry rather than hard-fail the
			// identity.
			return nil, ErrMCUpstreamUnavailable
		}
		profile.ID = strings.ToLower(strings.ReplaceAll(profile.ID, "-", ""))
		return &profile, nil

	case status == http.StatusUnauthorized || status == http.StatusForbidden:
		return nil, ErrMCVerificationFailed

	case status == http.StatusNotFound:
		// Distinguish "license but no profile yet" from "no license at all"
		// via the entitlements endpoint (checked only on a 404, per contract).
		owned, err := ms.hasLicense(ctx, mcAccessToken)
		if err != nil {
			return nil, ErrMCUpstreamUnavailable
		}
		if owned {
			return nil, ErrMCProfileMissing
		}
		return nil, ErrMCLicenseNotFound

	default:
		return nil, ErrMCUpstreamUnavailable
	}
}

// hasLicense reports whether the token's account owns a Minecraft license.
func (ms *MicrosoftService) hasLicense(ctx context.Context, mcAccessToken string) (bool, error) {
	status, body, err := ms.get(ctx, "/entitlements/mcstore", mcAccessToken)
	if err != nil {
		return false, err
	}
	if status != http.StatusOK {
		// A non-200 entitlements answer is treated as "no ownership proven";
		// only a decodable item list proves a license.
		return false, nil
	}
	var ent mcEntitlements
	if err := json.Unmarshal(body, &ent); err != nil {
		return false, nil
	}
	return len(ent.Items) > 0, nil
}

// get performs an authenticated GET and returns (status, body). Network
// errors, timeouts and read failures surface as a non-nil error.
func (ms *MicrosoftService) get(ctx context.Context, path, bearer string) (int, []byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, ms.base+path, nil)
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("Authorization", "Bearer "+bearer)
	req.Header.Set("Accept", "application/json")

	resp, err := ms.httpClient.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return 0, nil, err
	}
	return resp.StatusCode, body, nil
}

// String implements fmt.Stringer for logging without leaking the base token.
func (p *MCProfile) String() string {
	return fmt.Sprintf("uuid=%s name=%s", p.ID, p.Name)
}
