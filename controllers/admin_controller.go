package controllers

import (
	"errors"
	"log"
	"net/http"
	"net/mail"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/lnb/HRPAuth-Yggdrasil-API/clients"
	"github.com/lnb/HRPAuth-Yggdrasil-API/services"
)

// AdminController serves the operator/plugin administration endpoints under
// /admin (HA-Contract hrpauth-yggdrasil-business.yaml). Both endpoints are
// called by the Velocity plugin with an OAuth2 service token; they are
// relayed through the HRPAuth core under the /yggdrasil-api prefix.
type AdminController struct {
	adminSvc   *services.AdminService
	coreClient *clients.CoreClient
}

func NewAdminController() *AdminController {
	return &AdminController{
		adminSvc:   services.NewAdminService(),
		coreClient: clients.NewCoreClient(),
	}
}

// resolveServiceBearer resolves the request bearer against the core and
// requires an OAuth2 service token.
func (ac *AdminController) resolveServiceBearer(c *gin.Context) (*clients.TokenContext, bool) {
	bearer := strings.TrimSpace(extractBearer(c))
	if bearer == "" {
		respondError(c, http.StatusUnauthorized, CodeOAuthLoginRequired, "missing bearer token")
		return nil, false
	}

	ctx, err := ac.coreClient.ResolveToken(bearer)
	if err != nil || ctx == nil {
		respondError(c, http.StatusUnauthorized, CodeOAuthInvalidGrant, "invalid access token")
		return nil, false
	}
	if !ctx.IsService {
		respondError(c, http.StatusForbidden, CodeOAuthAccessDenied, "a service token is required")
		return nil, false
	}
	return ctx, true
}

type claimUserRequest struct {
	Username string `json:"username"`
	Email    string `json:"email"`
	Password string `json:"password"`
}

// ClaimUser handles POST /admin/claim-user: converts a proxy-registered game
// account (cbh=0) into a claimed one by provisioning its core user
// (email + password, verified) and linking it. Restored pre-split semantics
// of the monolith's POST /admin/claim-user.
func (ac *AdminController) ClaimUser(c *gin.Context) {
	tokenCtx, ok := ac.resolveServiceBearer(c)
	if !ok {
		return
	}
	if !tokenCtx.HasScope("user.claim.as-service") {
		respondError(c, http.StatusForbidden, CodeOAuthInsufficientScope, "insufficient scope")
		return
	}

	var req claimUserRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		respondError(c, http.StatusBadRequest, CodeInvalidJSONBody, "Invalid request body")
		return
	}

	username := strings.TrimSpace(req.Username)
	if len(username) < 3 {
		respondError(c, http.StatusBadRequest, CodeUsernameTooShort, "Username too short")
		return
	}
	email := strings.ToLower(strings.TrimSpace(req.Email))
	if !isValidEmailFormat(email) {
		respondError(c, http.StatusBadRequest, CodeInvalidEmail, "Invalid email")
		return
	}
	if len(req.Password) < 6 {
		respondError(c, http.StatusBadRequest, CodePasswordTooShort, "Password too short")
		return
	}

	account, profile, err := ac.adminSvc.FindAccountByProfileName(username)
	if err != nil {
		log.Printf("[YGG-BIZ] request_id=%s action=claim_user username=%s result=lookup_failed err=%v", c.GetString("request_id"), username, err)
		respondError(c, http.StatusInternalServerError, CodeInternalError, "Failed to look up account")
		return
	}
	if account == nil || profile == nil {
		respondError(c, http.StatusNotFound, CodeUserNotFound, "user not found")
		return
	}
	if account.CBH {
		respondError(c, http.StatusConflict, CodeUserNotClaimable, "user is not a proxy-registered account")
		return
	}

	coreUser, err := ac.coreClient.ClaimUser(username, email, req.Password)
	if err != nil {
		switch {
		case errors.Is(err, clients.ErrEmailAlreadyRegistered):
			respondError(c, http.StatusConflict, CodeEmailAlreadyRegistered, "Email already registered")
		case errors.Is(err, clients.ErrUsernameAlreadyBound):
			respondError(c, http.StatusConflict, CodeUsernameAlreadyBound, "Username already bound")
		default:
			log.Printf("[YGG-BIZ] request_id=%s action=claim_user username=%s result=core_failed err=%v", c.GetString("request_id"), username, err)
			respondError(c, http.StatusBadGateway, CodeInternalError, "core claim provisioning failed")
		}
		return
	}

	if err := ac.adminSvc.ClaimProxyAccount(account.ID, coreUser.UUID); err != nil {
		// The core user is already provisioned at this point. The core
		// endpoint is idempotent by (username, email), so a player retry
		// re-resolves the same core user and completes the link instead of
		// stranding it.
		log.Printf("[YGG-BIZ] request_id=%s action=claim_user username=%s result=link_failed err=%v", c.GetString("request_id"), username, err)
		respondError(c, http.StatusInternalServerError, CodeInternalError, "Failed to claim account")
		return
	}

	log.Printf("[YGG-BIZ] request_id=%s action=claim_user username=%s account_id=%d core_uid=%d result=success status=%d", c.GetString("request_id"), username, account.ID, coreUser.UID, http.StatusOK)
	respondOK(c, "User claimed", gin.H{
		"uid":      coreUser.UID,
		"username": username,
		"email":    email,
	})
}

type forceBindRequest struct {
	Username string `json:"username"`
	Email    string `json:"email"`
	Password string `json:"password"`
}

// ForceBind handles POST /admin/force-bind: transfers the mojang_uuid from a
// proxy-registered account (matched by profile name, cbh=0) to the account
// of the core user authenticated by email + password, then deletes the proxy
// account. Restored pre-split semantics of the monolith's
// POST /admin/force-bind, with the single-database transaction replaced by a
// read-only core verification plus one local transaction. Authentication uses
// the same service-token tier as ClaimUser (author correction 2026-11-05:
// the retired manage.token is no longer accepted).
func (ac *AdminController) ForceBind(c *gin.Context) {
	tokenCtx, ok := ac.resolveServiceBearer(c)
	if !ok {
		return
	}
	if !tokenCtx.HasScope("user.force-bind.as-service") {
		respondError(c, http.StatusForbidden, CodeOAuthInsufficientScope, "insufficient scope")
		return
	}

	var req forceBindRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		respondError(c, http.StatusBadRequest, CodeInvalidJSONBody, "Invalid request body")
		return
	}

	username := strings.TrimSpace(req.Username)
	if len(username) < 3 {
		respondError(c, http.StatusBadRequest, CodeUsernameTooShort, "Username too short")
		return
	}
	email := strings.ToLower(strings.TrimSpace(req.Email))
	if !isValidEmailFormat(email) {
		respondError(c, http.StatusBadRequest, CodeInvalidEmail, "Invalid email")
		return
	}
	if len(req.Password) < 6 {
		respondError(c, http.StatusBadRequest, CodePasswordTooShort, "Password too short")
		return
	}

	source, _, err := ac.adminSvc.FindAccountByProfileName(username)
	if err != nil {
		log.Printf("[YGG-BIZ] request_id=%s action=force_bind username=%s result=lookup_failed err=%v", c.GetString("request_id"), username, err)
		respondError(c, http.StatusInternalServerError, CodeInternalError, "Failed to look up account")
		return
	}
	if source == nil || source.CBH {
		respondError(c, http.StatusNotFound, CodeUserNotFound, "target proxy account not found")
		return
	}
	if source.MojangUUID == nil || *source.MojangUUID == "" {
		respondError(c, http.StatusBadRequest, CodeNoMojangUUID, "proxy account has no mojang_uuid to transfer")
		return
	}
	mojangUUID := *source.MojangUUID

	targetUser, err := ac.coreClient.VerifyCredentials(email, req.Password)
	if err != nil || targetUser == nil || targetUser.UUID == "" {
		respondError(c, http.StatusForbidden, CodeInvalidCredentials, "invalid credentials")
		return
	}

	if err := ac.adminSvc.ForceBindTransfer(source.ID, targetUser.UUID, mojangUUID); err != nil {
		if errors.Is(err, services.ErrMojangUUIDAlreadyBound) {
			respondError(c, http.StatusConflict, CodeMojangUUIDAlreadyBound, "target account already bound to another mojang_uuid")
			return
		}
		log.Printf("[YGG-BIZ] request_id=%s action=force_bind username=%s result=transfer_failed err=%v", c.GetString("request_id"), username, err)
		respondError(c, http.StatusInternalServerError, CodeInternalError, "Failed to transfer mojang_uuid")
		return
	}

	log.Printf("[YGG-BIZ] request_id=%s action=force_bind username=%s mojang_uuid=%s target_core_user=%s result=success status=%d", c.GetString("request_id"), username, mojangUUID, targetUser.UUID, http.StatusOK)
	respondOK(c, "Force bind successful", gin.H{
		"mojang_uuid": mojangUUID,
	})
}

// isValidEmailFormat mirrors the core's mail.ParseAddress-based check so a
// claim request cannot pass here with an address the core would reject.
func isValidEmailFormat(email string) bool {
	_, err := mail.ParseAddress(email)
	return err == nil
}
