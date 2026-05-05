package postgres

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"
)

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
	if _, err := db.ExecContext(ctx, sqlText); err != nil {
		return "", err
	}
	return sqlText, nil
}
