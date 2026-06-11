package postgres

import (
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5/pgconn"
)

// ServingRefreshSide identifies which database participated in a serving refresh step.
type ServingRefreshSide string

const (
	ServingRefreshSideSource ServingRefreshSide = "source"
	ServingRefreshSideTarget ServingRefreshSide = "target"
)

// ServingRefreshPhase names a high-level serving refresh step.
type ServingRefreshPhase string

const (
	ServingRefreshPhaseCopy ServingRefreshPhase = "copy"
)

// ServingRefreshCause classifies a sanitized serving refresh failure.
type ServingRefreshCause string

const (
	ServingRefreshCauseStatementTimeout ServingRefreshCause = "statement_timeout"
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
	object := e.Object
	if object == "" {
		object = "data"
	}
	return fmt.Sprintf("%s %s %s failed: %v", e.Side, object, e.Phase, e.Err)
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
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "57014" {
		return ServingRefreshCauseStatementTimeout
	}
	return ""
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
			"%s %s copy exceeded the Postgres statement_timeout; raise statement_timeout for the refresh role or session and rerun",
			side,
			object,
		)
	default:
		return fmt.Sprintf(
			"%s %s %s failed; inspect operator logs for the sanitized underlying error",
			side,
			object,
			e.Phase,
		)
	}
}
