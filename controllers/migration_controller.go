package controllers

import (
	"bytes"
	"context"
	"errors"
	"io"
	"mime/multipart"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/lnb/HRPAuth-Yggdrasil-API/clients"
	"github.com/lnb/HRPAuth-Yggdrasil-API/services"
)

const migrationCommitMaxBodyBytes int64 = 16 << 20
const migrationAssetMaxBytes int64 = 4 << 20

type MigrationCommitter interface {
	Commit(context.Context, services.MigrationCommitRequest) (services.MigrationCommitResult, error)
}

type MigrationController struct {
	core      *clients.CoreClient
	committer MigrationCommitter
}

func NewMigrationController(core *clients.CoreClient, committer MigrationCommitter) *MigrationController {
	return &MigrationController{core: core, committer: committer}
}

func (mc *MigrationController) Commit(c *gin.Context) {
	if _, ok := resolveServiceBearer(c, mc.core, "migration.commit.as-service"); !ok {
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, migrationCommitMaxBodyBytes)
	if err := c.Request.ParseMultipartForm(8 << 20); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			respondError(c, http.StatusRequestEntityTooLarge, CodeInvalidRequest, "Migration request exceeds size limit")
			return
		}
		respondError(c, http.StatusBadRequest, CodeInvalidRequest, "Invalid multipart migration request")
		return
	}
	if c.Request.MultipartForm == nil {
		respondError(c, http.StatusBadRequest, CodeInvalidRequest, "Invalid multipart migration request")
		return
	}
	defer c.Request.MultipartForm.RemoveAll()

	metadata, skin, cape, ok := readMigrationParts(c.Request.MultipartForm)
	if !ok {
		respondError(c, http.StatusBadRequest, CodeInvalidRequest, "Migration metadata and asset parts are invalid")
		return
	}
	decoded, err := services.DecodeMigrationMetadata(bytes.NewReader(metadata))
	if err != nil {
		respondMigrationError(c, err)
		return
	}
	if c.GetHeader("Idempotency-Key") != decoded.MigrationID {
		respondError(c, http.StatusBadRequest, CodeInvalidRequest, "Idempotency-Key must equal migration_id")
		return
	}
	request, err := services.NormalizeMigrationCommit(decoded, skin, cape)
	if err != nil {
		respondMigrationError(c, err)
		return
	}
	result, err := mc.committer.Commit(c.Request.Context(), request)
	if err != nil {
		respondMigrationError(c, err)
		return
	}
	respondOK(c, "Migration committed", result)
}

func readMigrationParts(form *multipart.Form) (metadata, skin, cape []byte, ok bool) {
	if len(form.Value) != 1 || len(form.Value["metadata"]) != 1 {
		return nil, nil, nil, false
	}
	if _, exists := form.Value["skin"]; exists {
		return nil, nil, nil, false
	}
	if _, exists := form.Value["cape"]; exists {
		return nil, nil, nil, false
	}
	for name, headers := range form.File {
		switch name {
		case "skin", "cape":
			if len(headers) != 1 {
				return nil, nil, nil, false
			}
		case "metadata":
			return nil, nil, nil, false
		default:
			return nil, nil, nil, false
		}
	}
	metadata = []byte(form.Value["metadata"][0])
	for _, part := range []struct {
		name   string
		target *[]byte
	}{{name: "skin", target: &skin}, {name: "cape", target: &cape}} {
		headers := form.File[part.name]
		if len(headers) == 0 {
			continue
		}
		file, err := headers[0].Open()
		if err != nil {
			return nil, nil, nil, false
		}
		data, readErr := io.ReadAll(io.LimitReader(file, migrationAssetMaxBytes+1))
		closeErr := file.Close()
		if readErr != nil || closeErr != nil || int64(len(data)) > migrationAssetMaxBytes {
			return nil, nil, nil, false
		}
		*part.target = data
	}
	return metadata, skin, cape, true
}

func respondMigrationError(c *gin.Context, err error) {
	var migrationErr *services.MigrationError
	if !errors.As(err, &migrationErr) {
		respondError(c, http.StatusInternalServerError, CodeInternalError, "Migration commit failed")
		return
	}
	switch migrationErr.Code {
	case "invalid_request", "invalid_email", "invalid_texture_file", "invalid_texture_model":
		respondError(c, http.StatusBadRequest, migrationErr.Code, "Invalid migration request")
	case "migration_conflict", "migration_credential_conflict", "migration_profile_conflict", "migration_asset_conflict", "migration_target_not_empty":
		respondError(c, http.StatusConflict, migrationErr.Code, "Migration conflicts with existing state")
	case "migration_commit_failed":
		respondError(c, http.StatusInternalServerError, migrationErr.Code, "Migration commit failed")
	default:
		respondError(c, http.StatusInternalServerError, CodeInternalError, "Migration commit failed")
	}
}
