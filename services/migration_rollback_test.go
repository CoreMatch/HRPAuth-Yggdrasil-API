package services

import (
	"context"
	"errors"
	"net/http"
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

func TestMigrationCoordinatorRollbackCompensatesCreatedCoreUser(t *testing.T) {
	coordinator, store, results, state, _, _ := setupMigrationCoordinator(t)
	if _, err := coordinator.Commit(context.Background(), coordinatorRequest(t, false)); err != nil {
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
	state.mu.Lock()
	coreStatus, userMissing, rollbackCalls := state.coreStatus, state.userMissing, state.rollbackCalls
	state.mu.Unlock()
	if coreStatus != "rolled_back" || !userMissing || rollbackCalls != 1 {
		t.Fatalf("Core compensation = status %q missing %v calls %d", coreStatus, userMissing, rollbackCalls)
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

func TestMigrationCoordinatorRollbackRecoversLostCoreResponse(t *testing.T) {
	coordinator, _, _, state, _, _ := setupMigrationCoordinator(t)
	if _, err := coordinator.Commit(context.Background(), coordinatorRequest(t, false)); err != nil {
		t.Fatal(err)
	}
	markMigrationIncomplete(t)
	state.mu.Lock()
	state.loseRollbackResponse = true
	state.mu.Unlock()

	_, err := coordinator.Rollback(context.Background(), coordinatorMigrationID)
	var migrationErr *MigrationError
	if !errors.As(err, &migrationErr) || migrationErr.Code != "migration_rollback_failed" {
		t.Fatalf("error = %v, want migration_rollback_failed", err)
	}
	var accountCount, profileCount int64
	if err := database.DB.Model(&models.Account{}).Count(&accountCount).Error; err != nil {
		t.Fatal(err)
	}
	if err := database.DB.Model(&models.Profile{}).Count(&profileCount).Error; err != nil {
		t.Fatal(err)
	}
	if accountCount != 0 || profileCount != 0 {
		t.Fatalf("local resources remain after lost response: accounts=%d profiles=%d", accountCount, profileCount)
	}
	state.mu.Lock()
	coreStatus, userMissing := state.coreStatus, state.userMissing
	state.mu.Unlock()
	if coreStatus != "rolled_back" || !userMissing {
		t.Fatalf("Core was not compensated before response loss: status=%q missing=%v", coreStatus, userMissing)
	}

	status, err := coordinator.Rollback(context.Background(), coordinatorMigrationID)
	if err != nil || status.Status != "rolled_back" {
		t.Fatalf("retry failed: status=%+v err=%v", status, err)
	}
	state.mu.Lock()
	rollbackCalls := state.rollbackCalls
	state.mu.Unlock()
	if rollbackCalls != 2 {
		t.Fatalf("rollback calls = %d, want 2", rollbackCalls)
	}
}

func TestMigrationCoordinatorRollbackRetriesAfterCoreFailure(t *testing.T) {
	coordinator, _, _, state, _, _ := setupMigrationCoordinator(t)
	if _, err := coordinator.Commit(context.Background(), coordinatorRequest(t, false)); err != nil {
		t.Fatal(err)
	}
	markMigrationIncomplete(t)
	state.mu.Lock()
	state.rollbackFailureStatus = http.StatusInternalServerError
	state.mu.Unlock()

	_, err := coordinator.Rollback(context.Background(), coordinatorMigrationID)
	var migrationErr *MigrationError
	if !errors.As(err, &migrationErr) || migrationErr.Code != "migration_rollback_failed" {
		t.Fatalf("error = %v, want migration_rollback_failed", err)
	}
	var journal models.MigrationJournal
	if err := database.DB.Where("migration_id = ?", coordinatorMigrationID).First(&journal).Error; err != nil {
		t.Fatal(err)
	}
	if journal.Status != "failed" || journal.RolledBackAt != nil {
		t.Fatalf("journal advanced after Core failure: %+v", journal)
	}
	state.mu.Lock()
	state.rollbackFailureStatus = 0
	state.mu.Unlock()

	status, err := coordinator.Rollback(context.Background(), coordinatorMigrationID)
	if err != nil || status.Status != "rolled_back" {
		t.Fatalf("retry failed: status=%+v err=%v", status, err)
	}
}

func TestMigrationCoordinatorRollbackFailsClosedForCreatedCoreOwnershipMismatch(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*coordinatorCoreState)
	}{
		{"missing journal", func(state *coordinatorCoreState) { state.migrationMissing = true }},
		{"identity mismatch", func(state *coordinatorCoreState) { state.userMismatch = true }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			coordinator, _, _, state, _, _ := setupMigrationCoordinator(t)
			if _, err := coordinator.Commit(context.Background(), coordinatorRequest(t, false)); err != nil {
				t.Fatal(err)
			}
			markMigrationIncomplete(t)
			state.mu.Lock()
			test.mutate(state)
			state.mu.Unlock()

			_, err := coordinator.Rollback(context.Background(), coordinatorMigrationID)
			var migrationErr *MigrationError
			if !errors.As(err, &migrationErr) || migrationErr.Code != "migration_rollback_not_allowed" {
				t.Fatalf("error = %v, want migration_rollback_not_allowed", err)
			}
			var accountCount, profileCount int64
			if err := database.DB.Model(&models.Account{}).Count(&accountCount).Error; err != nil {
				t.Fatal(err)
			}
			if err := database.DB.Model(&models.Profile{}).Count(&profileCount).Error; err != nil {
				t.Fatal(err)
			}
			if accountCount != 1 || profileCount != 1 {
				t.Fatalf("resources deleted before ownership resolved: accounts=%d profiles=%d", accountCount, profileCount)
			}
		})
	}
}
