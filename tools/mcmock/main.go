// Command mcmock is a local mock of the Microsoft / Mojang identity chain used
// to develop and end-to-end-test the Microsoft-login mapping before the Azure
// application and Mojang allowlist review are ready.
//
// It serves every hop of the public chain on one host so the client can point
// each configurable base (MSA / XBL / XSTS / Minecraft Services) at the same
// mock URL:
//
//	POST /consumers/oauth2/v2.0/devicecode     MSA device-code grant start
//	GET  /consumers/oauth2/v2.0/authorize      MSA authorization-code start
//	POST /consumers/oauth2/v2.0/token          MSA token endpoint
//	POST /user/authenticate                    Xbox Live
//	POST /xsts/authorize                       XSTS
//	POST /authentication/login_with_xbox       Minecraft Services login
//	GET  /minecraft/profile                    Minecraft Services profile
//	GET  /entitlements/mcstore                 Minecraft Services entitlements
//
// Test control endpoints (never part of the real chain):
//
//	GET  /_mock/personas                       list personas
//	POST /_mock/persona  {"id":"..."}          set the active persona
//
// The mock auto-approves sign-ins: the active persona is issued tokens for the
// whole chain. Personas cover the interesting cases (license+profile,
// license-only, none). All state is in-memory; restart resets it.
package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"flag"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"
)

// Persona is one fake Microsoft/Mojang identity.
type Persona struct {
	ID         string `json:"id"`
	Email      string `json:"email"`
	UHS        string `json:"-"` // Xbox user hash
	MCUUID     string `json:"mc_uuid"`
	MCName     string `json:"mc_name"`
	HasLicense bool   `json:"has_license"`
	HasProfile bool   `json:"has_profile"`
}

type mock struct {
	mu sync.Mutex
	// token -> persona id
	msaAccess  map[string]string
	msaRefresh map[string]string
	xbl        map[string]string
	xsts       map[string]string
	mc         map[string]string
	// device code -> persona id (pending)
	deviceCodes map[string]string
	// auth code -> persona id (pending)
	authCodes map[string]string

	personas map[string]*Persona
	active   string
}

func newMock() *mock {
	m := &mock{
		msaAccess:   map[string]string{},
		msaRefresh:  map[string]string{},
		xbl:         map[string]string{},
		xsts:        map[string]string{},
		mc:          map[string]string{},
		deviceCodes: map[string]string{},
		authCodes:   map[string]string{},
		personas:    map[string]*Persona{},
	}
	for _, p := range []*Persona{
		{ID: "default", Email: "genuine@example.com", UHS: "uhsGenuine",
			MCUUID: "f7c77d999f154a66a87dc4a51ef30d19", MCName: "GenuinePlayer",
			HasLicense: true, HasProfile: true},
		{ID: "noprofile", Email: "noprofile@example.com", UHS: "uhsNoProfile",
			MCUUID: "00000000000000000000000000000001", MCName: "",
			HasLicense: true, HasProfile: false},
		{ID: "nolicense", Email: "nolicense@example.com", UHS: "uhsNoLicense",
			MCUUID: "00000000000000000000000000000002", MCName: "",
			HasLicense: false, HasProfile: false},
	} {
		m.personas[p.ID] = p
	}
	m.active = "default"
	return m
}

func randHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func (m *mock) persona(id string) *Persona {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.personas[id]
}

func (m *mock) activePersona() *Persona {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.personas[m.active]
}

// --- MSA ---

func (m *mock) msaDeviceCode(w http.ResponseWriter, r *http.Request) {
	p := m.activePersona()
	code := randHex(16)
	m.mu.Lock()
	m.deviceCodes[code] = p.ID
	m.mu.Unlock()
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"device_code":      code,
		"user_code":        strings.ToUpper(randHex(4)),
		"verification_uri": "https://mock.local/device",
		"expires_in":       900,
		"interval":         1,
	})
}

func (m *mock) msaAuthorize(w http.ResponseWriter, r *http.Request) {
	// Auto-approve: redirect back to the loopback with a code.
	p := m.activePersona()
	code := randHex(16)
	m.mu.Lock()
	m.authCodes[code] = p.ID
	m.mu.Unlock()

	redirect := r.URL.Query().Get("redirect_uri")
	state := r.URL.Query().Get("state")
	if redirect == "" {
		http.Error(w, "missing redirect_uri", http.StatusBadRequest)
		return
	}
	loc := redirect + "?code=" + code
	if state != "" {
		loc += "&state=" + state
	}
	http.Redirect(w, r, loc, http.StatusFound)
}

func (m *mock) msaToken(w http.ResponseWriter, r *http.Request) {
	_ = r.ParseForm()
	grant := r.FormValue("grant_type")

	personaID := ""
	switch grant {
	case "urn:ietf:params:oauth:grant-type:device_code":
		dc := r.FormValue("device_code")
		m.mu.Lock()
		personaID, _ = m.deviceCodes[dc]
		delete(m.deviceCodes, dc)
		m.mu.Unlock()
	case "authorization_code":
		code := r.FormValue("code")
		m.mu.Lock()
		personaID, _ = m.authCodes[code]
		delete(m.authCodes, code)
		m.mu.Unlock()
	case "refresh_token":
		rt := r.FormValue("refresh_token")
		m.mu.Lock()
		personaID, _ = m.msaRefresh[rt]
		m.mu.Unlock()
	default:
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "unsupported_grant_type"})
		return
	}

	if personaID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_grant"})
		return
	}

	access := "msa-access-" + randHex(12)
	refresh := "msa-refresh-" + randHex(12)
	m.mu.Lock()
	m.msaAccess[access] = personaID
	m.msaRefresh[refresh] = personaID
	m.mu.Unlock()

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"token_type":    "Bearer",
		"scope":         "XboxLive.signin offline_access",
		"expires_in":    3600,
		"access_token":  access,
		"refresh_token": refresh,
	})
}

// --- Xbox Live / XSTS ---

func (m *mock) xblAuthenticate(w http.ResponseWriter, r *http.Request) {
	// Public Xbox Live shape: Properties.RpsTicket is the string "d=<msa>".
	var body struct {
		Properties struct {
			RpsTicket string `json:"RpsTicket"`
		} `json:"Properties"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad_request"})
		return
	}
	msaToken := strings.TrimPrefix(body.Properties.RpsTicket, "d=")
	if msaToken == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad_request"})
		return
	}
	m.mu.Lock()
	personaID, ok := m.msaAccess[msaToken]
	m.mu.Unlock()
	if !ok {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid_token"})
		return
	}
	p := m.persona(personaID)
	xblToken := "xbl-" + randHex(12)
	m.mu.Lock()
	m.xbl[xblToken] = personaID
	m.mu.Unlock()
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"Token":         xblToken,
		"DisplayClaims": map[string]interface{}{"xui": []map[string]string{{"uhs": p.UHS}}},
	})
}

func (m *mock) xstsAuthorize(w http.ResponseWriter, r *http.Request) {
	// Public XSTS shape: Properties.UserTokens carries the Xbox Live tokens.
	var body struct {
		Properties struct {
			UserTokens []string `json:"UserTokens"`
		} `json:"Properties"`
		RelyingParty string `json:"RelyingParty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad_request"})
		return
	}
	if len(body.Properties.UserTokens) == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad_request"})
		return
	}
	m.mu.Lock()
	personaID, ok := m.xbl[body.Properties.UserTokens[0]]
	m.mu.Unlock()
	if !ok {
		// Real XSTS returns 401 with an XErr payload; a bare 401 suffices here.
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid_token"})
		return
	}
	p := m.persona(personaID)
	xstsToken := "xsts-" + randHex(12)
	m.mu.Lock()
	m.xsts[xstsToken] = personaID
	m.mu.Unlock()
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"Token":         xstsToken,
		"DisplayClaims": map[string]interface{}{"xui": []map[string]string{{"uhs": p.UHS}}},
	})
}

// --- Minecraft Services ---

func (m *mock) mcLoginWithXbox(w http.ResponseWriter, r *http.Request) {
	var body struct {
		IdentityToken string `json:"identityToken"` // "XBL3.0 x=<uhs>;<xsts>"
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad_request"})
		return
	}
	// identityToken = "XBL3.0 x=<uhs>;<xsts>"
	xstsToken := ""
	if i := strings.Index(body.IdentityToken, ";"); i >= 0 {
		xstsToken = body.IdentityToken[i+1:]
	}
	m.mu.Lock()
	personaID, ok := m.xsts[xstsToken]
	m.mu.Unlock()
	if !ok {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid_token"})
		return
	}
	mcToken := "mc-" + randHex(12)
	m.mu.Lock()
	m.mc[mcToken] = personaID
	m.mu.Unlock()
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"access_token": mcToken,
		"token_type":   "Bearer",
		"expires_in":   86400,
	})
}

func (m *mock) bearerPersona(r *http.Request) *Persona {
	auth := r.Header.Get("Authorization")
	token := strings.TrimPrefix(auth, "Bearer ")
	m.mu.Lock()
	personaID, ok := m.mc[token]
	m.mu.Unlock()
	if !ok {
		return nil
	}
	return m.persona(personaID)
}

func (m *mock) mcProfile(w http.ResponseWriter, r *http.Request) {
	p := m.bearerPersona(r)
	if p == nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid_token"})
		return
	}
	if !p.HasProfile {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "NOT_FOUND"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"id":   p.MCUUID,
		"name": p.MCName,
	})
}

func (m *mock) mcEntitlements(w http.ResponseWriter, r *http.Request) {
	p := m.bearerPersona(r)
	if p == nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid_token"})
		return
	}
	items := []map[string]string{}
	if p.HasLicense {
		items = append(items, map[string]string{"name": "product_minecraft"})
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"items": items})
}

// --- test control ---

func (m *mock) listPersonas(w http.ResponseWriter, r *http.Request) {
	m.mu.Lock()
	list := make([]*Persona, 0, len(m.personas))
	for _, p := range m.personas {
		list = append(list, p)
	}
	active := m.active
	m.mu.Unlock()
	writeJSON(w, http.StatusOK, map[string]interface{}{"active": active, "personas": list})
}

func (m *mock) setPersona(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.ID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "id required"})
		return
	}
	m.mu.Lock()
	if _, ok := m.personas[body.ID]; !ok {
		m.mu.Unlock()
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "unknown persona"})
		return
	}
	m.active = body.ID
	m.mu.Unlock()
	writeJSON(w, http.StatusOK, map[string]string{"active": body.ID})
}

func main() {
	addr := flag.String("addr", "127.0.0.1:8088", "listen address")
	flag.Parse()

	m := newMock()
	mux := http.NewServeMux()
	mux.HandleFunc("/consumers/oauth2/v2.0/devicecode", m.msaDeviceCode)
	mux.HandleFunc("/consumers/oauth2/v2.0/authorize", m.msaAuthorize)
	mux.HandleFunc("/consumers/oauth2/v2.0/token", m.msaToken)
	mux.HandleFunc("/user/authenticate", m.xblAuthenticate)
	mux.HandleFunc("/xsts/authorize", m.xstsAuthorize)
	mux.HandleFunc("/authentication/login_with_xbox", m.mcLoginWithXbox)
	mux.HandleFunc("/minecraft/profile", m.mcProfile)
	mux.HandleFunc("/entitlements/mcstore", m.mcEntitlements)
	mux.HandleFunc("/_mock/personas", m.listPersonas)
	mux.HandleFunc("/_mock/persona", m.setPersona)

	log.Printf("mcmock listening on %s (active persona: %s)", *addr, m.active)
	srv := &http.Server{Addr: *addr, Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	if err := srv.ListenAndServe(); err != nil {
		log.Fatal(err)
	}
}
