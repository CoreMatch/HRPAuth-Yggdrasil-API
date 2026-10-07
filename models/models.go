package models

import (
	"time"
)

// Token represents the Yggdrasil authentication token.
// Moved from core to Yggdrasil API provider.
type Token struct {
	ID                int       `gorm:"primaryKey;autoIncrement;column:id"`
	AccessToken       string    `gorm:"type:varchar(255);uniqueIndex;column:access_token"`
	ClientToken       string    `gorm:"type:varchar(255);index:idx_tokens_client_token;column:client_token"`
	UserID            string    `gorm:"type:varchar(32);column:user_id;index"`
	SelectedProfileID string    `gorm:"type:varchar(32);column:selected_profile_id;index"`
	IssuedAt          int64     `gorm:"type:bigint(20);column:issued_at"`
	ExpiresInDays     int       `gorm:"default:15;column:expires_in_days"`
	State             string    `gorm:"type:enum('valid','temporarily_invalid','invalid');default:'valid';column:state"`
	CreatedAt         time.Time `gorm:"column:created_at"`
}

func (Token) TableName() string {
	return "tokens"
}

// Session represents the Yggdrasil join/hasJoined session.
// Moved from core to Yggdrasil API provider.
type Session struct {
	ID        int       `gorm:"primaryKey;autoIncrement;column:id"`
	ProfileID string    `gorm:"type:varchar(32);column:profile_id;index"`
	ServerID  string    `gorm:"type:varchar(255);column:server_id;index:idx_sessions_server_id"`
	IP        string    `gorm:"type:varchar(45);column:ip"`
	CreatedAt time.Time `gorm:"column:created_at"`
	ExpiresAt time.Time `gorm:"column:expires_at;index:idx_sessions_expires_at"`
}

func (Session) TableName() string {
	return "sessions"
}

// ProfileKey represents the chat-signing key pair issued to a user for the
// Minecraft Profile Key feature.
// Moved from core to Yggdrasil API provider.
type ProfileKey struct {
	ID                 int       `gorm:"primaryKey;autoIncrement;column:id"`
	UserID             string    `gorm:"type:varchar(32);column:user_id;uniqueIndex:uk_profile_keys_user_id"`
	PublicKey          string    `gorm:"type:text;column:public_key"`
	PrivateKey         string    `gorm:"type:text;column:private_key"`
	PublicKeySignature string    `gorm:"type:text;column:public_key_signature"`
	ExpiresAt          time.Time `gorm:"column:expires_at;index:idx_profile_keys_expires_at"`
	RefreshedAfter     time.Time `gorm:"column:refreshed_after"`
	CreatedAt          time.Time `gorm:"column:created_at"`
	UpdatedAt          time.Time `gorm:"column:updated_at"`
}

func (ProfileKey) TableName() string {
	return "profile_keys"
}

// --- DTOs for Core API Communication ---

// Account represents a Minecraft account linked to a core user.
// This establishes the 1:1 mapping between general auth and game auth.
type Account struct {
	ID         int        `gorm:"primaryKey;autoIncrement;column:id"`
	CoreUserID *string    `gorm:"type:varchar(32);uniqueIndex;column:core_user_id"`
	MojangUUID *string    `gorm:"type:varchar(32);column:mojang_uuid;uniqueIndex:uk_accounts_mojang_uuid"`
	Password   string     `gorm:"type:varchar(255);column:password"` // Only for proxy accounts
	MBE        bool       `gorm:"type:tinyint(1);not null;default:0;column:mbe"`
	CBH        bool       `gorm:"type:tinyint(1);not null;default:1;column:cbh"` // 1 = Human, 0 = Proxy/Bot
	RegisterAt time.Time  `gorm:"column:created_at"`
	LastSignAt *time.Time `gorm:"column:last_sign_at"`
	UpdatedAt  time.Time  `gorm:"column:updated_at"`
}

func (Account) TableName() string {
	return "accounts"
}

// Profile represents a Minecraft character.
type Profile struct {
	ID        string    `gorm:"primaryKey;type:varchar(32);column:id"`
	AccountID int       `gorm:"column:account_id;index"`
	Name      string    `gorm:"type:varchar(30);column:name"`
	Model     string    `gorm:"type:enum('default','slim');default:'default';column:model"`
	CreatedAt time.Time `gorm:"column:created_at"`
	UpdatedAt time.Time `gorm:"column:updated_at"`
}

func (Profile) TableName() string {
	return "profiles"
}

// ProfileProperty represents dynamic attributes of a profile (e.g., textures).
type ProfileProperty struct {
	ID         int    `gorm:"primaryKey;autoIncrement;column:id"`
	ProfileID  string `gorm:"type:varchar(32);column:profile_id;index"`
	Name       string `gorm:"type:varchar(255);column:name"`
	Value      string `gorm:"type:text;column:value"`
	Signature  string `gorm:"type:text;column:signature"`
	DeleteWhen int64  `gorm:"type:bigint;not null;default:0;column:delete_when"`
}

func (ProfileProperty) TableName() string {
	return "profile_properties"
}

// Texture represents the raw texture metadata.
type Texture struct {
	Hash string `json:"hash"`
	URL  string `json:"url"`
}

// --- DTOs for Core API Communication ---

type User struct {
	UID        uint   `json:"uid"`
	UUID       string `json:"uuid"`
	Email      string `json:"email"`
	Username   string `json:"username"`
	Permission int    `json:"permission"`
}
