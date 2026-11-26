package controllers

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/lnb/HRPAuth-Yggdrasil-API/clients"
)

func resolveServiceBearer(c *gin.Context, coreClient *clients.CoreClient, scope string) (*clients.TokenContext, bool) {
	bearer := strings.TrimSpace(extractBearer(c))
	if bearer == "" {
		respondError(c, http.StatusUnauthorized, CodeOAuthLoginRequired, "missing bearer token")
		return nil, false
	}
	tokenContext, err := coreClient.ResolveTokenContext(c.Request.Context(), bearer)
	if err != nil || tokenContext == nil {
		respondError(c, http.StatusUnauthorized, CodeOAuthInvalidGrant, "invalid access token")
		return nil, false
	}
	if !tokenContext.IsService {
		respondError(c, http.StatusForbidden, CodeOAuthAccessDenied, "a service token is required")
		return nil, false
	}
	if !tokenContext.HasScope(scope) {
		respondError(c, http.StatusForbidden, CodeOAuthInsufficientScope, "insufficient scope")
		return nil, false
	}
	return tokenContext, true
}
