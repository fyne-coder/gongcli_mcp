package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
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
	if err := validateScopedReaderRolePosture(ctx, db, params.RoleName); err != nil {
		return "", err
	}
	if _, err := db.ExecContext(ctx, `SET lock_timeout = '5s'; SET statement_timeout = '30s';`+sqlText); err != nil {
		return "", err
	}
	if err := validateScopedReaderEffectiveBoundary(ctx, db, params); err != nil {
		return "", err
	}
	return sqlText, nil
}

func validateScopedReaderRolePosture(ctx context.Context, db *sql.DB, roleName string) error {
	roleName = strings.TrimSpace(roleName)
	var inherit bool
	if err := db.QueryRowContext(ctx, `SELECT rolinherit FROM pg_roles WHERE rolname = $1`, roleName).Scan(&inherit); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("scoped reader role %q does not exist", roleName)
		}
		return err
	}
	if inherit {
		return fmt.Errorf("scoped reader role %q must be NOINHERIT before grants are applied", roleName)
	}
	outboundMemberships, err := scopedReaderRoleMemberships(ctx, db, roleName, true)
	if err != nil {
		return err
	}
	if len(outboundMemberships) > 0 {
		return fmt.Errorf("scoped reader role %q must not be a member of other roles: %s", roleName, strings.Join(outboundMemberships, ", "))
	}
	inboundMemberships, err := scopedReaderRoleMemberships(ctx, db, roleName, false)
	if err != nil {
		return err
	}
	if len(inboundMemberships) > 0 {
		return fmt.Errorf("scoped reader role %q must not be granted to other roles: %s", roleName, strings.Join(inboundMemberships, ", "))
	}
	return nil
}

func scopedReaderRoleMemberships(ctx context.Context, db *sql.DB, roleName string, outbound bool) ([]string, error) {
	query := `
SELECT parent.rolname
  FROM pg_auth_members m
  JOIN pg_roles parent ON parent.oid = m.roleid
  JOIN pg_roles member ON member.oid = m.member
 WHERE member.rolname = $1
 ORDER BY parent.rolname`
	if !outbound {
		query = `
SELECT member.rolname
  FROM pg_auth_members m
  JOIN pg_roles parent ON parent.oid = m.roleid
  JOIN pg_roles member ON member.oid = m.member
 WHERE parent.rolname = $1
 ORDER BY member.rolname`
	}
	rows, err := db.QueryContext(ctx, query, roleName)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var names []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		names = append(names, name)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return names, nil
}

func validateScopedReaderEffectiveBoundary(ctx context.Context, db *sql.DB, params ScopedReaderGrantSQLParams) error {
	options := ReadOnlyOptionsForToolAllowlist(params.Allowlist)
	if !options.EnforceAllowedColumnBoundary || !BusinessPilotScopedColumns(params.Allowlist) {
		return errors.New("scoped reader effective validation currently supports only the reviewed business-pilot scoped reader surface")
	}
	roleName := strings.TrimSpace(params.RoleName)
	missingColumnGrants, err := missingColumnSelectGrantsForRole(ctx, db, roleName, cleanColumnSelectGrants(options.RequiredColumnSelectGrants))
	if err != nil {
		return err
	}
	if len(missingColumnGrants) > 0 {
		return fmt.Errorf("scoped reader role %q is missing required column SELECT grants after apply: %s", roleName, strings.Join(missingColumnGrants, ", "))
	}
	extraColumnGrants, err := extraColumnSelectGrantsForRole(ctx, db, roleName, cleanColumnSelectGrants(options.AllowedColumnSelectGrants))
	if err != nil {
		return err
	}
	if len(extraColumnGrants) > 0 {
		return fmt.Errorf("scoped reader role %q has effective extra column SELECT grants after apply: %s", roleName, strings.Join(extraColumnGrants, ", "))
	}
	requiredFunctions := cleanPostgresFunctionSignatures(options.RequiredFunctionSignatures)
	missingFunctionGrants, err := missingFunctionExecuteGrantsForRole(ctx, db, roleName, requiredFunctions)
	if err != nil {
		return err
	}
	if len(missingFunctionGrants) > 0 {
		return fmt.Errorf("scoped reader role %q is missing required function EXECUTE grants after apply: %s", roleName, strings.Join(missingFunctionGrants, ", "))
	}
	extraFunctionGrants, err := extraFunctionExecuteGrantsForRole(ctx, db, roleName, cleanPostgresFunctionSignatures(options.AllowedFunctionSignatures))
	if err != nil {
		return err
	}
	if len(extraFunctionGrants) > 0 {
		return fmt.Errorf("scoped reader role %q has effective extra function EXECUTE grants after apply: %s", roleName, strings.Join(extraFunctionGrants, ", "))
	}
	publicFunctionGrants, err := publicGongMCPFunctionGrants(ctx, db)
	if err != nil {
		return err
	}
	if len(publicFunctionGrants) > 0 {
		return fmt.Errorf("public has over-broad gongmcp function EXECUTE grants after apply: %s", strings.Join(publicFunctionGrants, ", "))
	}
	return nil
}

func missingColumnSelectGrantsForRole(ctx context.Context, db *sql.DB, roleName string, required []ColumnSelectGrant) ([]string, error) {
	var missing []string
	for _, grant := range required {
		var exists, ok bool
		if err := db.QueryRowContext(ctx, `
SELECT EXISTS (
	SELECT 1
	  FROM pg_attribute a
	 WHERE a.attrelid = to_regclass('public.' || quote_ident($1))
	   AND a.attname = $2
	   AND NOT a.attisdropped
)`, grant.Table, grant.Column).Scan(&exists); err != nil {
			return nil, err
		}
		if !exists {
			missing = append(missing, displayColumnSelectGrant(grant.Table, grant.Column))
			continue
		}
		if err := db.QueryRowContext(ctx, `SELECT has_column_privilege($1, 'public.' || quote_ident($2), $3, 'SELECT')`, roleName, grant.Table, grant.Column).Scan(&ok); err != nil {
			return nil, err
		}
		if !ok {
			missing = append(missing, displayColumnSelectGrant(grant.Table, grant.Column))
		}
	}
	return missing, nil
}

func extraColumnSelectGrantsForRole(ctx context.Context, db *sql.DB, roleName string, allowed []ColumnSelectGrant) ([]string, error) {
	allowedSet := make(map[string]struct{}, len(allowed))
	for _, grant := range allowed {
		allowedSet[normalizeColumnSelectGrant(grant.Table, grant.Column)] = struct{}{}
	}
	rows, err := db.QueryContext(ctx, `
SELECT c.table_name, c.column_name
  FROM information_schema.columns c
 WHERE c.table_schema = 'public'
   AND has_column_privilege($1, quote_ident(c.table_schema) || '.' || quote_ident(c.table_name), c.column_name, 'SELECT')
 ORDER BY c.table_name, c.column_name`, roleName)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var extra []string
	for rows.Next() {
		var table, column string
		if err := rows.Scan(&table, &column); err != nil {
			return nil, err
		}
		if _, ok := allowedSet[normalizeColumnSelectGrant(table, column)]; !ok {
			extra = append(extra, displayColumnSelectGrant(table, column))
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return extra, nil
}

func missingFunctionExecuteGrantsForRole(ctx context.Context, db *sql.DB, roleName string, required []string) ([]string, error) {
	var missing []string
	for _, signature := range required {
		var ok bool
		if err := db.QueryRowContext(ctx, `SELECT has_function_privilege($1, $2, 'EXECUTE')`, roleName, signature).Scan(&ok); err != nil {
			return nil, err
		}
		if !ok {
			missing = append(missing, signature)
		}
	}
	return missing, nil
}

func extraFunctionExecuteGrantsForRole(ctx context.Context, db *sql.DB, roleName string, allowed []string) ([]string, error) {
	allowedSet := make(map[string]struct{}, len(allowed))
	for _, signature := range allowed {
		allowedSet[normalizePostgresFunctionSignature(signature)] = struct{}{}
	}
	rows, err := db.QueryContext(ctx, `
SELECT p.oid::regprocedure::text AS signature
  FROM pg_proc p
  JOIN pg_namespace n
    ON n.oid = p.pronamespace
 WHERE n.nspname = 'public'
   AND p.proname LIKE 'gongmcp_%'
   AND has_function_privilege($1, p.oid, 'EXECUTE')
 ORDER BY signature`, roleName)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var extra []string
	for rows.Next() {
		var signature string
		if err := rows.Scan(&signature); err != nil {
			return nil, err
		}
		if _, ok := allowedSet[normalizePostgresFunctionSignature(signature)]; !ok {
			extra = append(extra, displayPostgresFunctionSignature(signature))
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return extra, nil
}

func publicGongMCPFunctionGrants(ctx context.Context, db *sql.DB) ([]string, error) {
	rows, err := db.QueryContext(ctx, `
SELECT p.oid::regprocedure::text AS signature
  FROM pg_proc p
  JOIN pg_namespace n
    ON n.oid = p.pronamespace
 WHERE n.nspname = 'public'
   AND p.proname LIKE 'gongmcp_%'
   AND has_function_privilege('public', p.oid, 'EXECUTE')
 ORDER BY signature`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var signatures []string
	for rows.Next() {
		var signature string
		if err := rows.Scan(&signature); err != nil {
			return nil, err
		}
		signatures = append(signatures, displayPostgresFunctionSignature(signature))
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return signatures, nil
}
