package core

import (
	"context"
	"time"
)

// UserRole represents tenant authorization privileges.
type UserRole string

// Tenant authorization roles assigned to user accounts and verified tokens.
const (
	// UserRoleUser grants standard single-tenant self-service access.
	UserRoleUser UserRole = "user"

	// UserRoleAdmin grants administrative and system-level management privileges.
	UserRoleAdmin UserRole = "admin"
)

// SystemUserID is the canonical user identifier for shared system resources, built-in presets, and global skills.
const SystemUserID = "system"

// User represents an authenticated tenant account backed by an external identity provider.
type User struct {
	ID        string    `json:"id"`
	Email     string    `json:"email"`
	FullName  string    `json:"full_name"`
	Role      UserRole  `json:"role"`
	Tier      string    `json:"tier"`
	IsActive  bool      `json:"is_active"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// UserStore defines persistence operations for tenant user accounts.
type UserStore interface {
	User(ctx context.Context, id string) (*User, error)
	UpsertUser(ctx context.Context, user *User) error
}
