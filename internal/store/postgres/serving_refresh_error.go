package postgres

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"

	"github.com/jackc/pgx/v5/pgconn"
)

// ServingRefreshSide identifies which database participated in a serving refresh step.
type ServingRefreshSide string

const (
	ServingRefreshSideSource  ServingRefreshSide = "source"
	ServingRefreshSideTarget  ServingRefreshSide = "target"
	ServingRefreshSideServing ServingRefreshSide = "serving"
)

// ServingRefreshPhase names a high-level serving refresh step.
type ServingRefreshPhase string

const (
	ServingRefreshPhaseCount            ServingRefreshPhase = "count"
	ServingRefreshPhaseConnect          ServingRefreshPhase = "connect"
	ServingRefreshPhaseAudit            ServingRefreshPhase = "audit"
	ServingRefreshPhaseLock             ServingRefreshPhase = "lock"
	ServingRefreshPhaseTransaction      ServingRefreshPhase = "transaction"
	ServingRefreshPhaseTruncate         ServingRefreshPhase = "truncate"
	ServingRefreshPhaseCopy             ServingRefreshPhase = "copy"
	ServingRefreshPhaseReadModel        ServingRefreshPhase = "read_model"
	ServingRefreshPhaseGovernancePolicy ServingRefreshPhase = "governance_policy"
	ServingRefreshPhaseValidation       ServingRefreshPhase = "validation"
	ServingRefreshPhaseMarker           ServingRefreshPhase = "marker"
)

// ServingRefreshCause classifies a sanitized serving refresh failure.
type ServingRefreshCause string

const (
	ServingRefreshCauseStatementTimeout ServingRefreshCause = "statement_timeout"
	ServingRefreshCauseLockTimeout      ServingRefreshCause = "lock_timeout"
	ServingRefreshCausePermissionDenied ServingRefreshCause = "permission_denied"
	ServingRefreshCauseConnectionFailed ServingRefreshCause = "connection_failed"
	ServingRefreshCauseMigrationMissing ServingRefreshCause = "migration_missing"
	ServingRefreshCauseConfigInvalid    ServingRefreshCause = "config_invalid"
	ServingRefreshCauseValidationFailed ServingRefreshCause = "validation_failed"
	ServingRefreshCauseCanceled         ServingRefreshCause = "canceled"
	ServingRefreshCauseUnknown          ServingRefreshCause = "unknown"
)

// ServingRefreshPhaseError preserves serving refresh phase context for CLI
// classification while keeping the underlying driver error available via Unwrap.
type ServingRefreshPhaseError struct {
	Phase  ServingRefreshPhase
	Side   ServingRefreshSide
	Object string
	Cause  ServingRefreshCause
	Err    error
}

func (e *ServingRefreshPhaseError) Error() string {
	if e == nil {
		return ""
	}
	return e.operatorDetail()
}

func (e *ServingRefreshPhaseError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

func wrapServingRefreshPhaseError(side ServingRefreshSide, object string, phase ServingRefreshPhase, err error) error {
	if err == nil {
		return nil
	}
	return &ServingRefreshPhaseError{
		Phase:  phase,
		Side:   side,
		Object: object,
		Cause:  classifyServingRefreshCause(err),
		Err:    err,
	}
}

func classifyServingRefreshCause(err error) ServingRefreshCause {
	if err == nil {
		return ServingRefreshCauseUnknown
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch pgErr.Code {
		case "57014":
			return ServingRefreshCauseStatementTimeout
		case "55P03", "40P01":
			return ServingRefreshCauseLockTimeout
		case "42501":
			return ServingRefreshCausePermissionDenied
		case "42P01", "42703", "42883":
			return ServingRefreshCauseMigrationMissing
		case "28P01", "28000", "3D000", "53300":
			return ServingRefreshCauseConnectionFailed
		}
	}
	if errors.Is(err, context.Canceled) {
		return ServingRefreshCauseCanceled
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return ServingRefreshCauseStatementTimeout
	}
	var netErr net.Error
	if errors.As(err, &netErr) {
		return ServingRefreshCauseConnectionFailed
	}
	msg := strings.ToLower(err.Error())
	switch {
	case strings.Contains(msg, "connection refused") ||
		strings.Contains(msg, "no such host") ||
		strings.Contains(msg, "dial tcp") ||
		strings.Contains(msg, "password authentication failed") ||
		strings.Contains(msg, "pg_hba"):
		return ServingRefreshCauseConnectionFailed
	case strings.Contains(msg, "governance config"):
		return ServingRefreshCauseConfigInvalid
	case strings.HasPrefix(msg, "redacted serving database validation failed") ||
		strings.Contains(msg, " validation failed"):
		return ServingRefreshCauseValidationFailed
	}
	return ServingRefreshCauseUnknown
}

// ServingRefreshOperatorDetail returns sanitized operator guidance for a serving
// refresh failure when phase context is available. The detail never includes
// URLs, customer names, call IDs, transcript text, or raw driver payloads.
func ServingRefreshOperatorDetail(err error) string {
	var phaseErr *ServingRefreshPhaseError
	if !errors.As(err, &phaseErr) {
		return ""
	}
	return phaseErr.operatorDetail()
}

func (e *ServingRefreshPhaseError) operatorDetail() string {
	if e == nil {
		return ""
	}
	object := e.Object
	if object == "" {
		object = "data"
	}
	side := e.Side
	if side == "" {
		side = "serving"
	}
	switch {
	case e.Cause == ServingRefreshCauseStatementTimeout:
		return fmt.Sprintf(
			"%s %s %s timed out; raise statement_timeout for the refresh role or session when Postgres canceled the statement, then rerun",
			side,
			object,
			e.Phase,
		)
	case e.Cause == ServingRefreshCauseCanceled:
		return fmt.Sprintf("%s %s %s was canceled before completing; rerun the refresh when ready", side, object, e.Phase)
	case e.Cause == ServingRefreshCauseLockTimeout:
		return fmt.Sprintf("%s %s %s was blocked by a Postgres lock timeout or deadlock; rerun after the contending database work finishes", side, object, e.Phase)
	case e.Cause == ServingRefreshCausePermissionDenied:
		return fmt.Sprintf("%s %s %s failed because Postgres privileges are insufficient for this refresh step", side, object, e.Phase)
	case e.Cause == ServingRefreshCauseConnectionFailed:
		return fmt.Sprintf("%s %s %s failed because the database connection, network path, or credentials are unavailable", side, object, e.Phase)
	case e.Cause == ServingRefreshCauseMigrationMissing:
		return fmt.Sprintf("%s %s %s failed because required Postgres schema objects are missing or stale", side, object, e.Phase)
	case e.Cause == ServingRefreshCauseConfigInvalid:
		return fmt.Sprintf("%s %s %s failed because the governance configuration is invalid", side, object, e.Phase)
	case e.Cause == ServingRefreshCauseValidationFailed:
		return fmt.Sprintf("%s %s %s failed validation; rerun after correcting the refresh inputs", side, object, e.Phase)
	default:
		return fmt.Sprintf(
			"%s %s %s failed; run doctor postgres-deploy and rerun after correcting the failing phase",
			side,
			object,
			e.Phase,
		)
	}
}
