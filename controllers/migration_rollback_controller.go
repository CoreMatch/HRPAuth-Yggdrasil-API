package controllers

import (
	"context"

	"github.com/gin-gonic/gin"
	"github.com/lnb/HRPAuth-Yggdrasil-API/clients"
	"github.com/lnb/HRPAuth-Yggdrasil-API/services"
)

type MigrationRollbacker interface {
	Rollback(context.Context, string) (services.MigrationStatus, error)
}

type MigrationRollbackController struct {
	core       *clients.CoreClient
	rollbacker MigrationRollbacker
}

func NewMigrationRollbackController(core *clients.CoreClient, rollbacker MigrationRollbacker) *MigrationRollbackController {
	return &MigrationRollbackController{core: core, rollbacker: rollbacker}
}

func (mc *MigrationRollbackController) Rollback(c *gin.Context) {
	if _, ok := resolveServiceBearer(c, mc.core, "migration.rollback.as-service"); !ok {
		return
	}
	status, err := mc.rollbacker.Rollback(c.Request.Context(), c.Param("migration_id"))
	if err != nil {
		respondMigrationError(c, err)
		return
	}
	respondOK(c, "Migration rolled back", status)
}
