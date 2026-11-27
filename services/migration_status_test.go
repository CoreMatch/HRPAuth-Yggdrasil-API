package services

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/lnb/HRPAuth-Yggdrasil-API/database"
	"github.com/lnb/HRPAuth-Yggdrasil-API/models"
)

func TestMigrationCoordinatorReconcileCommittedInventory(t *testing.T) {
	coordinator, _, _, state, verifier, _ := setupMigrationCoordinator(t)
	if _, err := coordinator.Commit(context.Background(), coordinatorRequest(t, true)); err != nil {
		t.Fatal(err)
	}
	status, err := coordinator.Reconcile(context.Background(), coordinatorMigrationID)
	if err != nil {
		t.Fatal(err)
	}
	if status.Status != "committed" || status.LastError != nil || status.Result == nil || len(status.Resources) != 5 {
		t.Fatalf("unexpected status: %+v", status)
	}
	for _, resource := range status.Resources {
		if !resource.Present {
			t.Fatalf("committed resource is absent: %+v", resource)
		}
	}
	state.mu.Lock()
	claimCalls := state.claimCalls
	state.mu.Unlock()
	verifier.mu.Lock()
	applyCalls := verifier.applyCalls
	verifier.mu.Unlock()
	if claimCalls != 1 || applyCalls != 2 {
		t.Fatalf("reconcile mutated resources: claims=%d applies=%d", claimCalls, applyCalls)
	}
}

func TestMigrationCoordinatorReconcileIgnoresLoginActivity(t *testing.T) {
	coordinator, _, _, _, _, _ := setupMigrationCoordinator(t)
	if _, err := coordinator.Commit(context.Background(), coordinatorRequest(t, true)); err != nil {
		t.Fatal(err)
	}
	if err := database.DB.Model(&models.Account{}).Where("core_user_id = ?", coordinatorCoreUUID).Update("last_sign_at", time.Now()).Error; err != nil {
		t.Fatal(err)
	}
	if err := database.DB.Model(&models.MigrationJournal{}).Where("migration_id = ?", coordinatorMigrationID).Update("failure_code", "migration_reconcile_failed").Error; err != nil {
		t.Fatal(err)
	}
	status, err := coordinator.Reconcile(context.Background(), coordinatorMigrationID)
	if err != nil {
		t.Fatal(err)
	}
	if status.Status != "committed" || status.LastError != nil || status.Result == nil {
		t.Fatalf("login activity blocked committed reconciliation: %+v", status)
	}
}

func TestMigrationCoordinatorReconcileInterruptedMigrationAsFailed(t *testing.T) {
	coordinator, _, _, _, _, _ := setupMigrationCoordinator(t)
	if _, err := coordinator.Commit(context.Background(), coordinatorRequest(t, false)); err != nil {
		t.Fatal(err)
	}
	if err := database.DB.Model(&models.MigrationJournal{}).Where("migration_id = ?", coordinatorMigrationID).Updates(map[string]interface{}{
		"status": "applying", "stage": "verify", "failure_code": "", "committed_at": nil,
	}).Error; err != nil {
		t.Fatal(err)
	}
	status, err := coordinator.Reconcile(context.Background(), coordinatorMigrationID)
	if err != nil {
		t.Fatal(err)
	}
	if status.Status != "failed" || status.LastError == nil || status.LastError.Code != "migration_reconcile_failed" {
		t.Fatalf("unexpected interrupted status: %+v", status)
	}
	var journal models.MigrationJournal
	if err := database.DB.Where("migration_id = ?", coordinatorMigrationID).First(&journal).Error; err != nil {
		t.Fatal(err)
	}
	if journal.Status != "failed" || journal.Stage != "reconcile" {
		t.Fatalf("journal was not reconciled: %+v", journal)
	}
}

func TestMigrationCoordinatorReconcileKeepsCommittedStatusOnDrift(t *testing.T) {
	coordinator, _, _, state, _, _ := setupMigrationCoordinator(t)
	if _, err := coordinator.Commit(context.Background(), coordinatorRequest(t, false)); err != nil {
		t.Fatal(err)
	}
	state.mu.Lock()
	state.userMismatch = true
	state.mu.Unlock()
	status, err := coordinator.Reconcile(context.Background(), coordinatorMigrationID)
	if err != nil {
		t.Fatal(err)
	}
	if status.Status != "committed" || status.LastError == nil || status.LastError.Code != "migration_reconcile_failed" || status.Result != nil {
		t.Fatalf("unexpected drift status: %+v", status)
	}
	state.mu.Lock()
	state.userMismatch = false
	state.mu.Unlock()
	status, err = coordinator.Reconcile(context.Background(), coordinatorMigrationID)
	if err != nil {
		t.Fatal(err)
	}
	if status.Status != "committed" || status.LastError != nil || status.Result == nil {
		t.Fatalf("committed drift was not cleared: %+v", status)
	}
}

func TestMigrationCoordinatorReconcileStagedPackageWithoutResources(t *testing.T) {
	coordinator, store, _, _, _, _ := setupMigrationCoordinator(t)
	request := coordinatorRequest(t, false)
	packageData, err := StageMigrationPackage(request)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Stage(context.Background(), coordinatorMigrationID, packageData); err != nil {
		t.Fatal(err)
	}
	if err := database.DB.Create(&models.MigrationJournal{
		MigrationID: coordinatorMigrationID, Digest: packageData.Digest, Status: "staged_in_redis", Stage: "staged_in_redis",
		ProfileID: coordinatorProfileID, ProfileName: "Player",
	}).Error; err != nil {
		t.Fatal(err)
	}
	status, err := coordinator.Reconcile(context.Background(), coordinatorMigrationID)
	if err != nil {
		t.Fatal(err)
	}
	if status.Status != "staged_in_redis" || status.LastError != nil || len(status.Resources) != 1 || status.Resources[0].Present {
		t.Fatalf("unexpected staged status: %+v", status)
	}
}

func TestMigrationCoordinatorReconcileMissingJournal(t *testing.T) {
	coordinator, _, _, _, _, _ := setupMigrationCoordinator(t)
	_, err := coordinator.Reconcile(context.Background(), coordinatorMigrationID)
	var migrationErr *MigrationError
	if !errors.As(err, &migrationErr) || migrationErr.Code != "migration_not_found" {
		t.Fatalf("error = %v, want migration_not_found", err)
	}
}
