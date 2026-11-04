package repository

import (
	"context"
	"database/sql/driver"
	"errors"
	"fmt"
	"testing"

	"github.com/jackc/pgerrcode"
	"github.com/jackc/pgx/v5/pgconn"
)

func TestIsRetriablePgError(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{name: "connection failure (08006)", err: &pgconn.PgError{Code: pgerrcode.ConnectionFailure}, want: true},
		{name: "connection exception (08000)", err: &pgconn.PgError{Code: pgerrcode.ConnectionException}, want: true},
		{name: "database is starting up (57P03)", err: &pgconn.PgError{Code: pgerrcode.CannotConnectNow}, want: true},
		{name: "wrong password (28P01)", err: &pgconn.PgError{Code: pgerrcode.InvalidPassword}, want: false},
		{name: "unknown database (3D000)", err: &pgconn.PgError{Code: pgerrcode.InvalidCatalogName}, want: false},
		{name: "unique violation", err: &pgconn.PgError{Code: pgerrcode.UniqueViolation}, want: false},
		{name: "check violation", err: &pgconn.PgError{Code: pgerrcode.CheckViolation}, want: false},
		{name: "wrapped Class 08", err: fmt.Errorf("update gauge: %w", &pgconn.PgError{Code: pgerrcode.ConnectionFailure}), want: true},
		{name: "bad connection", err: driver.ErrBadConn, want: true},
		{name: "cancelled", err: context.Canceled, want: false},
		{name: "plain error", err: errors.New("boom"), want: false},
		{name: "nil", err: nil, want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isRetriablePgError(tt.err); got != tt.want {
				t.Fatalf("isRetriablePgError(%v) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}
