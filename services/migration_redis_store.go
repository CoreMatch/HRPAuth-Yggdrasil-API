package services

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"time"

	goredis "github.com/redis/go-redis/v9"
)

const migrationKeyPrefix = "yggdrasil:migration:v1:"

type MigrationStoreOptions struct {
	TTL             time.Duration
	LockTTL         time.Duration
	MaxPackageBytes int64
}

type RedisMigrationStore struct {
	client          *goredis.Client
	ttl             time.Duration
	lockTTL         time.Duration
	maxPackageBytes int64
}

func NewRedisMigrationStore(client *goredis.Client, options MigrationStoreOptions) (*RedisMigrationStore, error) {
	if client == nil {
		return nil, errors.New("Redis client is required")
	}
	if options.TTL < 0 || options.LockTTL < 0 || options.MaxPackageBytes < 0 {
		return nil, errors.New("migration store limits must not be negative")
	}
	if options.TTL == 0 {
		options.TTL = 7 * 24 * time.Hour
	}
	if options.LockTTL == 0 {
		options.LockTTL = 10 * time.Minute
	}
	if options.MaxPackageBytes == 0 {
		options.MaxPackageBytes = 16 << 20
	}
	if options.TTL < time.Millisecond || options.LockTTL < time.Millisecond || options.MaxPackageBytes < 1 {
		return nil, errors.New("migration store limits must be positive")
	}
	return &RedisMigrationStore{client: client, ttl: options.TTL, lockTTL: options.LockTTL, maxPackageBytes: options.MaxPackageBytes}, nil
}

func (s *RedisMigrationStore) Acquire(ctx context.Context, migrationID string) (func(), error) {
	if !ValidateMigrationID(migrationID) {
		return nil, ErrInvalidMigrationRequest
	}
	token := make([]byte, 32)
	if _, err := rand.Read(token); err != nil {
		return nil, err
	}
	value := hex.EncodeToString(token)
	acquired, err := s.client.SetNX(ctx, s.packageKey(migrationID)+"lock", value, s.lockTTL).Result()
	if err != nil {
		return nil, err
	}
	if !acquired {
		return nil, ErrMigrationLocked
	}
	return func() {
		releaseCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = s.client.Eval(releaseCtx, `if redis.call("get", KEYS[1]) == ARGV[1] then return redis.call("del", KEYS[1]) else return 0 end`, []string{s.packageKey(migrationID) + "lock"}, value).Err()
	}, nil
}

func (s *RedisMigrationStore) Stage(ctx context.Context, migrationID string, migration StagedMigrationPackage) error {
	if !ValidateMigrationID(migrationID) || migration.Metadata.MigrationID != migrationID {
		return ErrInvalidMigrationRequest
	}
	encoded, err := EncodeStagedMigration(migration)
	if err != nil {
		return err
	}
	if int64(len(encoded)) > s.maxPackageBytes {
		return ErrMigrationTooLarge
	}
	key := s.packageKey(migrationID)
	stored, err := s.client.SetNX(ctx, key, encoded, s.ttl).Result()
	if err != nil {
		return err
	}
	if stored {
		return nil
	}
	existing, err := s.Load(ctx, migrationID)
	if err != nil {
		return err
	}
	if existing.Digest != migration.Digest {
		return ErrStagedConflict
	}
	return nil
}

func (s *RedisMigrationStore) Load(ctx context.Context, migrationID string) (StagedMigrationPackage, error) {
	if !ValidateMigrationID(migrationID) {
		return StagedMigrationPackage{}, ErrInvalidMigrationRequest
	}
	encoded, err := s.client.Get(ctx, s.packageKey(migrationID)).Bytes()
	if errors.Is(err, goredis.Nil) {
		return StagedMigrationPackage{}, ErrMigrationNotFound
	}
	if err != nil {
		return StagedMigrationPackage{}, err
	}
	if int64(len(encoded)) > s.maxPackageBytes {
		return StagedMigrationPackage{}, ErrMigrationTooLarge
	}
	return DecodeStagedMigration(bytes.NewReader(encoded))
}

func (s *RedisMigrationStore) Delete(ctx context.Context, migrationID string) error {
	if !ValidateMigrationID(migrationID) {
		return ErrInvalidMigrationRequest
	}
	return s.client.Del(ctx, s.packageKey(migrationID)).Err()
}

func (s *RedisMigrationStore) packageKey(migrationID string) string {
	return migrationKeyPrefix + migrationID + ":package"
}
