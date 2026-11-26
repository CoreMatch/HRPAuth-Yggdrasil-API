package controllers

import (
	"context"

	"github.com/gin-gonic/gin"
	"github.com/lnb/HRPAuth-Yggdrasil-API/clients"
	"github.com/lnb/HRPAuth-Yggdrasil-API/services"
)

type MigrationReconciler interface {
	Reconcile(context.Context, string) (services.MigrationStatus, error)
}

type MigrationStatusController struct {
	core       *clients.CoreClient
	reconciler MigrationReconciler
}

func NewMigrationStatusController(core *clients.CoreClient, reconciler MigrationReconciler) *MigrationStatusController {
	return &MigrationStatusController{core: core, reconciler: reconciler}
}

func (mc *MigrationStatusController) Reconcile(c *gin.Context) {
	if _, ok := resolveServiceBearer(c, mc.core, "migration.reconcile.as-service"); !ok {
		return
	}
	status, err := mc.reconciler.Reconcile(c.Request.Context(), c.Param("migration_id"))
	if err != nil {
		respondMigrationError(c, err)
		return
	}
	respondOK(c, "Migration reconciled", status)
}
