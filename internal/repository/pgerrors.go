package repository

import (
	"context"
	"database/sql/driver"
	"errors"

	"github.com/jackc/pgerrcode"
	"github.com/jackc/pgx/v5/pgconn"
)

// isRetriablePgError reports whether a PostgreSQL operation failed for a
// temporary transport reason and may be repeated:
//
//   - the server answered with a Class 08 (Connection Exception) error, or
//     with 57P03 while it is still starting up;
//   - the connection could not be established (*pgconn.ConnectError): the
//     server is down, restarting or still starting. This is the most common
//     case, and it is not a *pgconn.PgError, since the server never answered;
//   - database/sql reports driver.ErrBadConn after its own reconnects, which
//     a driver may only return when nothing was sent;
//   - pgconn says the request was never sent (pgconn.SafeToRetry).
//
// A connection lost after the request was sent is not retried: the statement
// (or COMMIT) may already have been applied, and repeating a counter upsert
// would add its delta twice. Errors in the data itself (constraint
// violations and the like) and cancellations are not retried either.
func isRetriablePgError(err error) bool {
	if err == nil || errors.Is(err, context.Canceled) {
		return false
	}

	// Checked before ConnectError: a failed connect that the server answered
	// wraps its *pgconn.PgError, and most such answers (a wrong password, an
	// unknown database) will not go away by themselves.
	if pgErr, ok := errors.AsType[*pgconn.PgError](err); ok {
		return pgerrcode.IsConnectionException(pgErr.Code) ||
			// 57P03 "the database system is starting up": not Class 08, but
			// exactly the temporary state retries are for.
			pgErr.Code == pgerrcode.CannotConnectNow
	}

	if _, ok := errors.AsType[*pgconn.ConnectError](err); ok {
		return true
	}

	if errors.Is(err, driver.ErrBadConn) {
		return true
	}

	return pgconn.SafeToRetry(err)
}
