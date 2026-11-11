// Package model defines all persistent entities. Models are dialect-neutral so the
// SQLite development database can be migrated to PostgreSQL/MySQL without code changes.
package model

import (
	"database/sql/driver"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/schema"
)

// Base carries the UUIDv7 primary key and timestamps shared by every table.
type Base struct {
	ID        string    `gorm:"primaryKey;size:36" json:"id"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

func NewID() string {
	id, err := uuid.NewV7()
	if err != nil {
		return uuid.NewString()
	}
	return id.String()
}

func (b *Base) BeforeCreate(*gorm.DB) error {
	if b.ID == "" {
		b.ID = NewID()
	}
	return nil
}

// JSON stores any Go value as JSON text (jsonb on PostgreSQL).
type JSON[T any] struct {
	V T
}

func J[T any](v T) JSON[T] { return JSON[T]{V: v} }

func (j JSON[T]) Value() (driver.Value, error) {
	b, err := json.Marshal(j.V)
	if err != nil {
		return nil, err
	}
	return string(b), nil
}

func (j *JSON[T]) Scan(src any) error {
	var b []byte
	switch v := src.(type) {
	case nil:
		var zero T
		j.V = zero
		return nil
	case string:
		b = []byte(v)
	case []byte:
		b = v
	default:
		return errors.New("model.JSON: unsupported source type")
	}
	if len(b) == 0 {
		var zero T
		j.V = zero
		return nil
	}
	return json.Unmarshal(b, &j.V)
}

func (j JSON[T]) MarshalJSON() ([]byte, error)  { return json.Marshal(j.V) }
func (j *JSON[T]) UnmarshalJSON(b []byte) error { return json.Unmarshal(b, &j.V) }

func (JSON[T]) GormDataType() string { return "json" }

func (JSON[T]) GormDBDataType(db *gorm.DB, _ *schema.Field) string {
	switch db.Dialector.Name() {
	case "postgres":
		return "jsonb"
	case "mysql":
		return "json"
	default:
		return "text"
	}
}

// StringList is a convenience alias for JSON string arrays.
type StringList = JSON[[]string]

func Strings(v ...string) StringList {
	if v == nil {
		v = []string{}
	}
	return StringList{V: v}
}

// Map is a free-form JSON object.
type Map = JSON[map[string]any]

func EmptyMap() Map { return Map{V: map[string]any{}} }
