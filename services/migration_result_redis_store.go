package services

import (
	"bytes"
	"context"
	"errors"
	"time"

	goredis "github.com/redis/go-redis/v9"
)

const migrationResultMaxBytes int64 = 1 << 20

type RedisMigrationResultStore struct {
	client *goredis.Client
	ttl    time.Duration
}

func NewRedisMigrationResultStore(client *goredis.Client, ttl time.Duration) (*RedisMigrationResultStore, error) {
	if client == nil {
		return nil, errors.New("Redis client is required")
	}
	if ttl < 0 {
		return nil, errors.New("migration result TTL must not be negative")
	}
	if ttl == 0 {
		ttl = 7 * 24 * time.Hour
	}
	if ttl < time.Millisecond {
		return nil, errors.New("migration result TTL must be positive")
	}
	return &RedisMigrationResultStore{client: client, ttl: ttl}, nil
}

func (s *RedisMigrationResultStore) Save(ctx context.Context, result MigrationCommitResult) error {
	encoded, err := EncodeMigrationResult(result)
	if err != nil {
		return err
	}
	if int64(len(encoded)) > migrationResultMaxBytes {
		return ErrMigrationTooLarge
	}
	return s.client.Set(ctx, s.resultKey(result.MigrationID), encoded, s.ttl).Err()
}

func (s *RedisMigrationResultStore) Load(ctx context.Context, migrationID string) (MigrationCommitResult, error) {
	if !ValidateMigrationID(migrationID) {
		return MigrationCommitResult{}, ErrInvalidMigrationRequest
	}
	encoded, err := s.client.Get(ctx, s.resultKey(migrationID)).Bytes()
	if errors.Is(err, goredis.Nil) {
		return MigrationCommitResult{}, ErrMigrationResultNotFound
	}
	if err != nil {
		return MigrationCommitResult{}, err
	}
	if int64(len(encoded)) > migrationResultMaxBytes {
		return MigrationCommitResult{}, ErrMigrationTooLarge
	}
	result, err := DecodeMigrationResult(bytes.NewReader(encoded))
	if err != nil {
		return MigrationCommitResult{}, err
	}
	if result.MigrationID != migrationID {
		return MigrationCommitResult{}, ErrMigrationResultNotFound
	}
	return result, nil
}

func (s *RedisMigrationResultStore) Delete(ctx context.Context, migrationID string) error {
	if !ValidateMigrationID(migrationID) {
		return ErrInvalidMigrationRequest
	}
	return s.client.Del(ctx, s.resultKey(migrationID)).Err()
}

func (s *RedisMigrationResultStore) resultKey(migrationID string) string {
	return migrationKeyPrefix + migrationID + ":result"
}
