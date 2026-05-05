package postgres

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"
)

const scopedReaderApplyTimeout = 30 * time.Second

func ApplyScopedReaderGrants(ctx context.Context, databaseURL string, params ScopedReaderGrantSQLParams) (string, error) {
	if strings.TrimSpace(databaseURL) == "" {
		return "", errors.New("postgres database URL is required")
	}
	sqlText, err := BuildScopedReaderGrantSQL(params)
	if err != nil {
		return "", err
	}
	db, err := sql.Open("pgx", databaseURL)
	if err != nil {
		return "", err
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	db.SetConnMaxLifetime(30 * time.Minute)
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, scopedReaderApplyTimeout)
		defer cancel()
	}
	var currentDatabase string
	if err := db.QueryRowContext(ctx, `SELECT current_database()`).Scan(&currentDatabase); err != nil {
		return "", err
	}
	if strings.TrimSpace(params.DatabaseName) != currentDatabase {
		return "", errors.New("postgres --database does not match connected database")
	}
	if _, err := db.ExecContext(ctx, `SET lock_timeout = '5s'; SET statement_timeout = '30s';`+sqlText); err != nil {
		return "", err
	}
	return sqlText, nil
}
