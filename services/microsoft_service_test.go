package services

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/lnb/HRPAuth-Yggdrasil-API/config"
)

// pointMicrosoftAt aims the Minecraft Services re-verification base at a test
// server for the duration of one test.
func pointMicrosoftAt(t *testing.T, base string) {
	t.Helper()
	if config.AppConfig == nil {
		config.AppConfig = &config.Config{}
	}
	old := config.AppConfig.Yggdrasil.Microsoft
	config.AppConfig.Yggdrasil.Microsoft.MinecraftServicesBase = base
	config.AppConfig.Yggdrasil.Microsoft.TimeoutSeconds = 5
	t.Cleanup(func() { config.AppConfig.Yggdrasil.Microsoft = old })
}

func TestVerifyMCTokenSuccess(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/minecraft/profile" {
			http.NotFound(w, r)
			return
		}
		if r.Header.Get("Authorization") != "Bearer good-token" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"id":"ABCDEF0123456789ABCDEF0123456789","name":"GenuinePlayer"}`))
	}))
	defer srv.Close()
	pointMicrosoftAt(t, srv.URL)

	profile, err := NewMicrosoftService().VerifyMCToken(context.Background(), "good-token")
	if err != nil {
		t.Fatalf("VerifyMCToken: %v", err)
	}
	if profile.ID != "abcdef0123456789abcdef0123456789" {
		t.Errorf("ID = %q, want lowercased 32-hex", profile.ID)
	}
	if profile.Name != "GenuinePlayer" {
		t.Errorf("Name = %q, want GenuinePlayer", profile.Name)
	}
}

func TestVerifyMCTokenRejected(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()
	pointMicrosoftAt(t, srv.URL)

	_, err := NewMicrosoftService().VerifyMCToken(context.Background(), "bad-token")
	if !errors.Is(err, ErrMCVerificationFailed) {
		t.Fatalf("err = %v, want ErrMCVerificationFailed", err)
	}
}

func TestVerifyMCTokenProfileMissingWithLicense(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/minecraft/profile":
			w.WriteHeader(http.StatusNotFound)
		case "/entitlements/mcstore":
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"items":[{"name":"product_minecraft"}]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	pointMicrosoftAt(t, srv.URL)

	_, err := NewMicrosoftService().VerifyMCToken(context.Background(), "token")
	if !errors.Is(err, ErrMCProfileMissing) {
		t.Fatalf("err = %v, want ErrMCProfileMissing", err)
	}
}

func TestVerifyMCTokenLicenseNotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/minecraft/profile":
			w.WriteHeader(http.StatusNotFound)
		case "/entitlements/mcstore":
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"items":[]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	pointMicrosoftAt(t, srv.URL)

	_, err := NewMicrosoftService().VerifyMCToken(context.Background(), "token")
	if !errors.Is(err, ErrMCLicenseNotFound) {
		t.Fatalf("err = %v, want ErrMCLicenseNotFound", err)
	}
}

func TestVerifyMCTokenUpstreamUnavailable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	pointMicrosoftAt(t, srv.URL)

	_, err := NewMicrosoftService().VerifyMCToken(context.Background(), "token")
	if !errors.Is(err, ErrMCUpstreamUnavailable) {
		t.Fatalf("err = %v, want ErrMCUpstreamUnavailable", err)
	}
}

func TestVerifyMCTokenEmpty(t *testing.T) {
	pointMicrosoftAt(t, "http://127.0.0.1:0")
	_, err := NewMicrosoftService().VerifyMCToken(context.Background(), "   ")
	if !errors.Is(err, ErrMCVerificationFailed) {
		t.Fatalf("err = %v, want ErrMCVerificationFailed for empty token", err)
	}
}
