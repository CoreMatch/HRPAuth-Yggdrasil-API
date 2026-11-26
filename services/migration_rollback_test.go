package services

import (
	"context"
	"errors"
	"testing"

	"github.com/lnb/HRPAuth-Yggdrasil-API/database"
	"github.com/lnb/HRPAuth-Yggdrasil-API/models"
)

func markMigrationIncomplete(t *testing.T) {
	t.Helper()
	if err := database.DB.Model(&models.MigrationJournal{}).Where("migration_id = ?", coordinatorMigrationID).Updates(map[string]interface{}{
		"status": "failed", "stage": "verify", "committed_at": nil, "failure_code": "migration_commit_failed",
	}).Error; err != nil {
		t.Fatal(err)
	}
}

func TestMigrationCoordinatorRollbackPreexistingCoreIsIdempotent(t *testing.T) {
	coordinator, store, results, state, _, core := setupMigrationCoordinator(t)
	state.mu.Lock()
	state.created = true
	state.mu.Unlock()
	if _, err := coordinator.Commit(context.Background(), coordinatorRequest(t, true)); err != nil {
		t.Fatal(err)
	}
	markMigrationIncomplete(t)
	status, err := coordinator.Rollback(context.Background(), coordinatorMigrationID)
	if err != nil {
		t.Fatal(err)
	}
	if status.Status != "rolled_back" || status.LastError != nil {
		t.Fatalf("unexpected rollback status: %+v", status)
	}
	var accountCount, profileCount int64
	if err := database.DB.Model(&models.Account{}).Count(&accountCount).Error; err != nil {
		t.Fatal(err)
	}
	if err := database.DB.Model(&models.Profile{}).Count(&profileCount).Error; err != nil {
		t.Fatal(err)
	}
	if accountCount != 0 || profileCount != 0 {
		t.Fatalf("rollback left local resources: accounts=%d profiles=%d", accountCount, profileCount)
	}
	if _, err := core.InspectMigrationUser(context.Background(), coordinatorCoreUUID); err != nil {
		t.Fatalf("preexisting Core user was removed: %v", err)
	}
	if _, err := store.Load(context.Background(), coordinatorMigrationID); !errors.Is(err, ErrMigrationNotFound) {
		t.Fatalf("staged package was not removed: %v", err)
	}
	if _, err := results.Load(context.Background(), coordinatorMigrationID); !errors.Is(err, ErrMigrationResultNotFound) {
		t.Fatalf("cached result was not removed: %v", err)
	}
	status, err = coordinator.Rollback(context.Background(), coordinatorMigrationID)
	if err != nil || status.Status != "rolled_back" {
		t.Fatalf("rollback retry failed: status=%+v err=%v", status, err)
	}
}

func TestMigrationCoordinatorRollbackRejectsCommittedMigration(t *testing.T) {
	coordinator, _, _, _, _, _ := setupMigrationCoordinator(t)
	if _, err := coordinator.Commit(context.Background(), coordinatorRequest(t, false)); err != nil {
		t.Fatal(err)
	}
	_, err := coordinator.Rollback(context.Background(), coordinatorMigrationID)
	var migrationErr *MigrationError
	if !errors.As(err, &migrationErr) || migrationErr.Code != "migration_rollback_not_allowed" {
		t.Fatalf("error = %v, want migration_rollback_not_allowed", err)
	}
	var accountCount int64
	if err := database.DB.Model(&models.Account{}).Count(&accountCount).Error; err != nil {
		t.Fatal(err)
	}
	if accountCount != 1 {
		t.Fatalf("committed resources were modified: accounts=%d", accountCount)
	}
}

func TestMigrationCoordinatorRollbackFailsClosedForCreatedCoreUser(t *testing.T) {
	coordinator, _, _, _, _, _ := setupMigrationCoordinator(t)
	if _, err := coordinator.Commit(context.Background(), coordinatorRequest(t, false)); err != nil {
		t.Fatal(err)
	}
	markMigrationIncomplete(t)
	_, err := coordinator.Rollback(context.Background(), coordinatorMigrationID)
	var migrationErr *MigrationError
	if !errors.As(err, &migrationErr) || migrationErr.Code != "migration_rollback_not_allowed" {
		t.Fatalf("error = %v, want migration_rollback_not_allowed", err)
	}
	var accountCount int64
	if err := database.DB.Model(&models.Account{}).Count(&accountCount).Error; err != nil {
		t.Fatal(err)
	}
	if accountCount != 1 {
		t.Fatalf("resources were deleted before Core ownership was resolved: accounts=%d", accountCount)
	}
}

func TestMigrationCoordinatorRollbackFailsClosedAfterAccountMutation(t *testing.T) {
	coordinator, _, _, state, _, _ := setupMigrationCoordinator(t)
	state.mu.Lock()
	state.created = true
	state.mu.Unlock()
	if _, err := coordinator.Commit(context.Background(), coordinatorRequest(t, false)); err != nil {
		t.Fatal(err)
	}
	markMigrationIncomplete(t)
	if err := database.DB.Model(&models.Account{}).Where("core_user_id = ?", coordinatorCoreUUID).Update("skin_sync", true).Error; err != nil {
		t.Fatal(err)
	}
	_, err := coordinator.Rollback(context.Background(), coordinatorMigrationID)
	var migrationErr *MigrationError
	if !errors.As(err, &migrationErr) || migrationErr.Code != "migration_rollback_not_allowed" {
		t.Fatalf("error = %v, want migration_rollback_not_allowed", err)
	}
	var account models.Account
	if err := database.DB.Where("core_user_id = ?", coordinatorCoreUUID).First(&account).Error; err != nil {
		t.Fatalf("modified account was deleted: %v", err)
	}
}

func TestMigrationCoordinatorRollbackFailsClosedWhenCoreOwnershipIsMissing(t *testing.T) {
	coordinator, _, _, state, _, _ := setupMigrationCoordinator(t)
	state.mu.Lock()
	state.created = true
	state.mu.Unlock()
	if _, err := coordinator.Commit(context.Background(), coordinatorRequest(t, false)); err != nil {
		t.Fatal(err)
	}
	markMigrationIncomplete(t)
	state.mu.Lock()
	state.userMissing = true
	state.mu.Unlock()
	_, err := coordinator.Rollback(context.Background(), coordinatorMigrationID)
	var migrationErr *MigrationError
	if !errors.As(err, &migrationErr) || migrationErr.Code != "migration_rollback_not_allowed" {
		t.Fatalf("error = %v, want migration_rollback_not_allowed", err)
	}
	var profileCount int64
	if err := database.DB.Model(&models.Profile{}).Count(&profileCount).Error; err != nil {
		t.Fatal(err)
	}
	if profileCount != 1 {
		t.Fatalf("resources were deleted with missing Core ownership: profiles=%d", profileCount)
	}
}

func TestMigrationCoordinatorRollbackRejectsInvalidID(t *testing.T) {
	coordinator, _, _, _, _, _ := setupMigrationCoordinator(t)
	_, err := coordinator.Rollback(context.Background(), "not-a-uuid")
	var migrationErr *MigrationError
	if !errors.As(err, &migrationErr) || migrationErr.Code != "invalid_request" {
		t.Fatalf("error = %v, want invalid_request", err)
	}
}
