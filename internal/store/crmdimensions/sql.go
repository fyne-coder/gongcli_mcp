package crmdimensions

import (
	"fmt"
	"sort"
	"strings"
)

func quoteSQLString(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "''") + "'"
}

// SQLiteObjectFieldExtractLines returns MAX(CASE...) lines for the SQLite
// object_fields CTE used by call_facts.
func SQLiteObjectFieldExtractLines() string {
	var lines []string
	for _, field := range PromotedFields {
		when := make([]string, 0, len(field.CRMFieldNames))
		for _, name := range field.CRMFieldNames {
			when = append(when, fmt.Sprintf("f.field_name = %s", quoteSQLString(name)))
		}
		expr := "TRIM(f.field_value_text)"
		if field.Kind == KindNumeric {
			expr = sqliteNumericExtractExpr("f.field_value_text")
		} else if field.Kind == KindDate {
			expr = sqliteDateExtractExpr("f.field_value_text")
		} else if field.Kind == KindBoolean {
			expr = sqliteBooleanExtractExpr("f.field_value_text")
		}
		lines = append(lines, fmt.Sprintf(
			"\t       COALESCE(MAX(CASE WHEN %s THEN %s END), %s) AS %s,",
			strings.Join(when, " OR "),
			expr,
			sqliteDefaultLiteral(field.Kind),
			field.Column,
		))
	}
	return strings.Join(lines, "\n")
}

func sqliteDefaultLiteral(kind ValueKind) string {
	switch kind {
	case KindNumeric:
		return "0"
	default:
		return "''"
	}
}

func sqliteNumericExtractExpr(valueExpr string) string {
	return fmt.Sprintf(`CAST(COALESCE(NULLIF(REPLACE(REPLACE(TRIM(%s), ',', ''), '$', ''), ''), '0') AS INTEGER)`, valueExpr)
}

func sqliteDateExtractExpr(valueExpr string) string {
	return fmt.Sprintf(`CASE WHEN length(TRIM(%s)) >= 10 THEN substr(TRIM(%s), 1, 10) ELSE '' END`, valueExpr, valueExpr)
}

func sqliteBooleanExtractExpr(valueExpr string) string {
	return fmt.Sprintf(`CASE
		       WHEN lower(TRIM(%s)) IN ('true', '1', 'yes', 'y') THEN 'true'
		       WHEN lower(TRIM(%s)) IN ('false', '0', 'no', 'n') THEN 'false'
		       ELSE ''
	       END`, valueExpr, valueExpr)
}

// SQLiteCallFactsAccountSelectLines returns COALESCE(a.column, default) lines.
func SQLiteCallFactsAccountSelectLines() string {
	var lines []string
	for _, field := range PromotedFields {
		if field.Scope != ScopeAccount {
			continue
		}
		lines = append(lines, fmt.Sprintf(
			"       COALESCE(a.%s, %s) AS %s,",
			field.Column,
			sqliteDefaultLiteral(field.Kind),
			field.Column,
		))
	}
	return strings.Join(lines, "\n")
}

// SQLiteCallFactsOpportunitySelectLines returns COALESCE(o.column, default) lines.
func SQLiteCallFactsOpportunitySelectLines() string {
	var lines []string
	for _, field := range PromotedFields {
		if field.Scope != ScopeOpportunity {
			continue
		}
		lines = append(lines, fmt.Sprintf(
			"       COALESCE(o.%s, %s) AS %s,",
			field.Column,
			sqliteDefaultLiteral(field.Kind),
			field.Column,
		))
	}
	return strings.Join(lines, "\n")
}

// PostgresCRMPromotionLinesForScope returns promotion lines for one object scope.
func PostgresCRMPromotionLinesForScope(scope ObjectScope) string {
	var lines []string
	for _, field := range PromotedFields {
		if field.Scope != scope {
			continue
		}
		when := make([]string, 0, len(field.CRMFieldNames))
		for _, name := range field.CRMFieldNames {
			when = append(when, fmt.Sprintf("f.field_name = %s", quoteSQLString(name)))
		}
		objectKey := "sa.object_key"
		if field.Scope == ScopeOpportunity {
			objectKey = "so.object_key"
		}
		expr := "TRIM(f.field_value_text)"
		if field.Kind == KindNumeric {
			expr = postgresNumericExtractExpr("f.field_value_text")
		} else if field.Kind == KindDate {
			expr = postgresDateExtractExpr("f.field_value_text")
		} else if field.Kind == KindBoolean {
			expr = postgresBooleanExtractExpr("f.field_value_text")
		}
		lines = append(lines, fmt.Sprintf(
			"\t       COALESCE(MAX(CASE WHEN f.object_key = %s AND (%s) THEN %s END), %s) AS %s,",
			objectKey,
			strings.Join(when, " OR "),
			expr,
			postgresDefaultLiteral(field.Kind),
			field.Column,
		))
	}
	return strings.Join(lines, "\n")
}

// PostgresSignalsCRMSelectLinesForScope returns signals CTE select lines.
func PostgresSignalsCRMSelectLinesForScope(scope ObjectScope) string {
	var lines []string
	for _, field := range PromotedFields {
		if field.Scope != scope {
			continue
		}
		lines = append(lines, fmt.Sprintf(
			"\t       COALESCE(crm.%s, %s) AS %s,",
			field.Column,
			postgresDefaultLiteral(field.Kind),
			field.Column,
		))
	}
	return strings.Join(lines, "\n")
}

// PostgresInsertCRMColumnNamesForScope returns comma-prefixed INSERT column names.
func PostgresInsertCRMColumnNamesForScope(scope ObjectScope) string {
	var cols []string
	for _, field := range PromotedFields {
		if field.Scope != scope {
			continue
		}
		cols = append(cols, field.Column)
	}
	if len(cols) == 0 {
		return ""
	}
	return ", " + strings.Join(cols, ", ")
}

// PostgresInsertCRMSelectLinesForScope returns final INSERT SELECT lines.
func PostgresInsertCRMSelectLinesForScope(scope ObjectScope) string {
	var lines []string
	for _, field := range PromotedFields {
		if field.Scope != scope {
			continue
		}
		lines = append(lines, fmt.Sprintf("\t       c.%s,", field.Column))
	}
	return strings.Join(lines, "\n")
}

func postgresDefaultLiteral(kind ValueKind) string {
	switch kind {
	case KindNumeric:
		return "0"
	default:
		return "''"
	}
}

func postgresNumericExtractExpr(valueExpr string) string {
	return fmt.Sprintf(`COALESCE(NULLIF(regexp_replace(TRIM(%s), '[^0-9.-]', '', 'g'), ''), '0')::numeric::bigint`, valueExpr)
}

func postgresDateExtractExpr(valueExpr string) string {
	return fmt.Sprintf(`CASE WHEN length(TRIM(%s)) >= 10 THEN left(TRIM(%s), 10) ELSE '' END`, valueExpr, valueExpr)
}

func postgresBooleanExtractExpr(valueExpr string) string {
	return fmt.Sprintf(`CASE
		       WHEN lower(TRIM(%s)) IN ('true', '1', 'yes', 'y') THEN 'true'
		       WHEN lower(TRIM(%s)) IN ('false', '0', 'no', 'n') THEN 'false'
		       ELSE ''
	       END`, valueExpr, valueExpr)
}

// PostgresAlterTableAddColumnStatements returns migration ALTER TABLE lines.
func PostgresAlterTableAddColumnStatements() string {
	seen := make(map[string]struct{})
	var lines []string
	for _, field := range PromotedFields {
		if _, ok := seen[field.Column]; ok {
			continue
		}
		seen[field.Column] = struct{}{}
		colType := "TEXT NOT NULL DEFAULT ''"
		if field.Kind == KindNumeric {
			colType = "BIGINT NOT NULL DEFAULT 0"
		}
		lines = append(lines, fmt.Sprintf("ALTER TABLE call_facts ADD COLUMN IF NOT EXISTS %s %s;", field.Column, colType))
	}
	sort.Strings(lines)
	return strings.Join(lines, "\n")
}

// PostgresCallFactsInsertColumns returns additional INSERT column names.
func PostgresCallFactsInsertColumns() string {
	cols := AllPromotedColumns()
	sort.Strings(cols)
	return strings.Join(cols, ", ")
}

// PostgresCallFactsSelectColumns returns c.column references for INSERT SELECT.
func PostgresCallFactsSelectColumns() string {
	var lines []string
	for _, col := range AllPromotedColumns() {
		field, _ := LookupPromotedField(col)
		lines = append(lines, fmt.Sprintf("       COALESCE(crm.%s, %s) AS %s,", col, postgresDefaultLiteral(field.Kind), col))
	}
	return strings.Join(lines, "\n")
}

// PostgresCallFactsCRMSelectColumns returns crm.column references in final SELECT.
func PostgresCallFactsCRMSelectColumns() string {
	var lines []string
	for _, col := range AllPromotedColumns() {
		field, _ := LookupPromotedField(col)
		lines = append(lines, fmt.Sprintf("       COALESCE(crm.%s, %s) AS %s,", col, postgresDefaultLiteral(field.Kind), col))
	}
	return strings.Join(lines, "\n")
}

// PostgresCallFactsSelectLines returns promoted call_facts columns for the
// Postgres business-pilot facts CTE.
func PostgresCallFactsSelectLines() string {
	var lines []string
	for _, field := range PromotedFields {
		lines = append(lines, fmt.Sprintf("       %s,", field.Column))
	}
	return strings.Join(lines, "\n")
}

// PostgresReaderGrantColumns returns comma-separated call_facts columns for
// least-privilege reader grants.
func PostgresReaderGrantColumns() string {
	base := []string{
		"call_id", "title", "started_at", "call_date", "call_month", "duration_seconds", "duration_bucket",
		"system", "direction", "scope", "purpose", "calendar_event_present", "transcript_present", "transcript_status",
		"lifecycle_bucket", "likely_voicemail_or_ivr", "lifecycle_confidence", "lifecycle_reason", "lifecycle_evidence_fields",
		"account_type", "account_industry", "account_revenue_range", "opportunity_stage", "opportunity_type",
		"opportunity_forecast_category", "opportunity_primary_lead_source", "opportunity_count", "account_count",
	}
	cols := append(base, AllPromotedColumns()...)
	sort.Strings(cols)
	return strings.Join(cols, ", ")
}

func qualifyColumn(callFactsAlias, column string) string {
	if strings.TrimSpace(callFactsAlias) == "" {
		return column
	}
	return callFactsAlias + "." + column
}

func sqliteNumericColumnExpr(callFactsAlias, column string) string {
	col := qualifyColumn(callFactsAlias, column)
	switch column {
	case "opportunity_amount", "opportunity_probability":
		return sqliteNumericExtractExpr(col)
	default:
		return col
	}
}

func postgresNumericColumnExpr(callFactsAlias, column string) string {
	col := qualifyColumn(callFactsAlias, column)
	switch column {
	case "opportunity_amount", "opportunity_probability":
		return postgresNumericExtractExpr(col)
	default:
		return col
	}
}

// SQLiteBucketExpr returns a SQLite expression for a bucket dimension.
func SQLiteBucketExpr(bucket BucketDimension, callFactsAlias string) string {
	col := sqliteNumericColumnExpr(callFactsAlias, bucket.SourceColumn)
	switch bucket.Kind {
	case BucketRevenueUSD:
		return sqliteRevenueBucketExpr(col)
	case BucketEmployeeCount:
		return sqliteEmployeeCountBucketExpr(col)
	case BucketProbability:
		return sqliteProbabilityBucketExpr(col)
	case BucketGenericNumeric:
		return sqliteGenericNumericBucketExpr(col)
	case BucketDateMonth:
		dateCol := qualifyColumn(callFactsAlias, bucket.SourceColumn)
		return fmt.Sprintf("CASE WHEN length(TRIM(%s)) >= 7 THEN substr(TRIM(%s), 1, 7) ELSE '' END", dateCol, dateCol)
	case BucketDateQuarter:
		dateCol := qualifyColumn(callFactsAlias, bucket.SourceColumn)
		return sqliteQuarterFromDateExpr(dateCol)
	default:
		return "''"
	}
}

// PostgresBucketExpr returns a Postgres expression for a bucket dimension.
func PostgresBucketExpr(bucket BucketDimension, callFactsAlias string) string {
	col := postgresNumericColumnExpr(callFactsAlias, bucket.SourceColumn)
	switch bucket.Kind {
	case BucketRevenueUSD:
		return postgresRevenueBucketExpr(col)
	case BucketEmployeeCount:
		return postgresEmployeeCountBucketExpr(col)
	case BucketProbability:
		return postgresProbabilityBucketExpr(col)
	case BucketGenericNumeric:
		return postgresGenericNumericBucketExpr(col)
	case BucketDateMonth:
		dateCol := qualifyColumn(callFactsAlias, bucket.SourceColumn)
		return fmt.Sprintf("CASE WHEN length(TRIM(%s)) >= 7 THEN left(TRIM(%s), 7) ELSE '' END", dateCol, dateCol)
	case BucketDateQuarter:
		dateCol := qualifyColumn(callFactsAlias, bucket.SourceColumn)
		return postgresQuarterFromDateExpr(dateCol)
	default:
		return "''"
	}
}

func sqliteRevenueBucketExpr(col string) string {
	return fmt.Sprintf(`CASE
		WHEN %s IS NULL OR %s <= 0 THEN 'unknown'
		WHEN %s < 1000000 THEN 'under_1m'
		WHEN %s < 10000000 THEN '1m_10m'
		WHEN %s < 100000000 THEN '10m_100m'
		ELSE '100m_plus'
	END`, col, col, col, col, col)
}

func postgresRevenueBucketExpr(col string) string {
	return fmt.Sprintf(`CASE
		WHEN %s IS NULL OR %s <= 0 THEN 'unknown'
		WHEN %s < 1000000 THEN 'under_1m'
		WHEN %s < 10000000 THEN '1m_10m'
		WHEN %s < 100000000 THEN '10m_100m'
		ELSE '100m_plus'
	END`, col, col, col, col, col)
}

func sqliteEmployeeCountBucketExpr(col string) string {
	return fmt.Sprintf(`CASE
		WHEN %s IS NULL OR %s <= 0 THEN 'unknown'
		WHEN %s < 50 THEN 'under_50'
		WHEN %s < 250 THEN '50_249'
		WHEN %s < 1000 THEN '250_999'
		WHEN %s < 5000 THEN '1000_4999'
		ELSE '5000_plus'
	END`, col, col, col, col, col, col)
}

func postgresEmployeeCountBucketExpr(col string) string {
	return sqliteEmployeeCountBucketExpr(col)
}

func sqliteProbabilityBucketExpr(col string) string {
	return fmt.Sprintf(`CASE
		WHEN %s IS NULL OR TRIM(CAST(%s AS TEXT)) = '' THEN 'unknown'
		WHEN CAST(%s AS INTEGER) <= 0 THEN '0'
		WHEN CAST(%s AS INTEGER) < 25 THEN '1_24'
		WHEN CAST(%s AS INTEGER) < 50 THEN '25_49'
		WHEN CAST(%s AS INTEGER) < 75 THEN '50_74'
		WHEN CAST(%s AS INTEGER) < 100 THEN '75_99'
		ELSE '100'
	END`, col, col, col, col, col, col, col)
}

func postgresProbabilityBucketExpr(col string) string {
	return fmt.Sprintf(`CASE
		WHEN %s IS NULL OR TRIM(CAST(%s AS TEXT)) = '' THEN 'unknown'
		WHEN %s <= 0 THEN '0'
		WHEN %s < 25 THEN '1_24'
		WHEN %s < 50 THEN '25_49'
		WHEN %s < 75 THEN '50_74'
		WHEN %s < 100 THEN '75_99'
		ELSE '100'
	END`, col, col, col, col, col, col, col)
}

func sqliteGenericNumericBucketExpr(col string) string {
	return fmt.Sprintf(`CASE
		WHEN %s IS NULL OR %s <= 0 THEN 'unknown'
		WHEN %s < 1000 THEN 'under_1k'
		WHEN %s < 10000 THEN '1k_10k'
		WHEN %s < 100000 THEN '10k_100k'
		ELSE '100k_plus'
	END`, col, col, col, col, col)
}

func postgresGenericNumericBucketExpr(col string) string {
	return sqliteGenericNumericBucketExpr(col)
}

func sqliteQuarterFromDateExpr(dateExpr string) string {
	return fmt.Sprintf(`CASE
		WHEN substr(%s, 6, 2) IN ('01','02','03') THEN substr(%s, 1, 4) || '-Q1'
		WHEN substr(%s, 6, 2) IN ('04','05','06') THEN substr(%s, 1, 4) || '-Q2'
		WHEN substr(%s, 6, 2) IN ('07','08','09') THEN substr(%s, 1, 4) || '-Q3'
		WHEN substr(%s, 6, 2) IN ('10','11','12') THEN substr(%s, 1, 4) || '-Q4'
		ELSE ''
	END`, dateExpr, dateExpr, dateExpr, dateExpr, dateExpr, dateExpr, dateExpr, dateExpr)
}

func postgresQuarterFromDateExpr(dateExpr string) string {
	return fmt.Sprintf(`CASE
		WHEN substring(%s from 6 for 2) IN ('01','02','03') THEN left(%s, 4) || '-Q1'
		WHEN substring(%s from 6 for 2) IN ('04','05','06') THEN left(%s, 4) || '-Q2'
		WHEN substring(%s from 6 for 2) IN ('07','08','09') THEN left(%s, 4) || '-Q3'
		WHEN substring(%s from 6 for 2) IN ('10','11','12') THEN left(%s, 4) || '-Q4'
		ELSE ''
	END`, dateExpr, dateExpr, dateExpr, dateExpr, dateExpr, dateExpr, dateExpr, dateExpr)
}

// PostgresCRMFilterDimensionAllowListCSV returns quoted CRM dimensions for SQL allow lists.
func PostgresCRMFilterDimensionAllowListCSV() string {
	return PostgresDimensionAllowListCSV(SupportedFilterDimensionNames())
}

// PostgresCRMStringEqualsDimensionsCSV returns CRM dimensions using equals/in matching.
func PostgresCRMStringEqualsDimensionsCSV() string {
	var dims []string
	for _, field := range PromotedFields {
		if field.Kind == KindCategorical {
			dims = append(dims, field.Column)
		}
	}
	for _, bucket := range BucketDimensions {
		dims = append(dims, bucket.Dimension)
	}
	sort.Strings(dims)
	return PostgresDimensionAllowListCSV(dims)
}

// PostgresCRMDateFilterDimensionsCSV returns date CRM dimensions using ISO date comparisons.
func PostgresCRMDateFilterDimensionsCSV() string {
	var dims []string
	for _, field := range PromotedFields {
		if field.Kind == KindDate {
			dims = append(dims, field.Column)
		}
	}
	return PostgresDimensionAllowListCSV(dims)
}

// PostgresCRMBooleanFilterDimensionsCSV returns boolean CRM dimensions.
func PostgresCRMBooleanFilterDimensionsCSV() string {
	var dims []string
	for _, field := range PromotedFields {
		if field.Kind == KindBoolean {
			dims = append(dims, field.Column)
		}
	}
	return PostgresDimensionAllowListCSV(dims)
}

// PostgresCRMNumericFilterDimensionsCSV returns numeric CRM dimensions.
func PostgresCRMNumericFilterDimensionsCSV() string {
	var dims []string
	for _, field := range PromotedFields {
		if field.Kind == KindNumeric {
			dims = append(dims, field.Column)
		}
	}
	return PostgresDimensionAllowListCSV(dims)
}

// PostgresCRMBooleanFilterCaseLines returns boolean_value CASE lines.
func PostgresCRMBooleanFilterCaseLines() string {
	var lines []string
	for _, field := range PromotedFields {
		if field.Kind != KindBoolean {
			continue
		}
		lines = append(lines, fmt.Sprintf("\t\t       WHEN %s THEN CASE WHEN cf.%s IN ('true', '1', 'yes', 'y') THEN true WHEN cf.%s IN ('false', '0', 'no', 'n') THEN false ELSE NULL END", quoteSQLString(field.Column), field.Column, field.Column))
	}
	return strings.Join(lines, "\n")
}

// PostgresDimensionFilterCaseLines returns WHEN lines inside
// gongmcp_business_analysis_dimension_filters_match normalized CASE.
func PostgresDimensionFilterCaseLines() string {
	var lines []string
	for _, field := range PromotedFields {
		if field.Kind == KindNumeric {
			continue
		}
		lines = append(lines, fmt.Sprintf("\t\t       WHEN %s THEN cf.%s", quoteSQLString(field.Column), field.Column))
	}
	for _, bucket := range BucketDimensions {
		lines = append(lines, fmt.Sprintf("\t\t       WHEN %s THEN %s", quoteSQLString(bucket.Dimension), PostgresBucketExpr(bucket, "cf")))
	}
	return strings.Join(lines, "\n")
}

// PostgresDimensionFilterNumericCaseLines returns numeric_value WHEN lines.
func PostgresDimensionFilterNumericCaseLines() string {
	var lines []string
	for _, field := range PromotedFields {
		if field.Kind != KindNumeric {
			continue
		}
		lines = append(lines, fmt.Sprintf("\t\t       WHEN %s THEN cf.%s", quoteSQLString(field.Column), field.Column))
	}
	return strings.Join(lines, "\n")
}

// PostgresDimensionFilterDateCaseLines returns date filter WHEN lines (string compare).
func PostgresDimensionFilterDateCaseLines() string {
	var lines []string
	for _, field := range PromotedFields {
		if field.Kind != KindDate {
			continue
		}
		lines = append(lines, fmt.Sprintf("\t\t       WHEN %s THEN cf.%s", quoteSQLString(field.Column), field.Column))
	}
	return strings.Join(lines, "\n")
}

// PostgresSummarizeDimensionCaseLines returns dimension_value CASE lines.
func PostgresSummarizeDimensionCaseLines() string {
	var lines []string
	for _, field := range PromotedFields {
		if field.Kind != KindCategorical && field.Kind != KindBoolean {
			continue
		}
		lines = append(lines, fmt.Sprintf("\t\t       WHEN %s THEN cf.%s", quoteSQLString(field.Column), field.Column))
	}
	for _, bucket := range BucketDimensions {
		lines = append(lines, fmt.Sprintf("\t\t       WHEN %s THEN %s", quoteSQLString(bucket.Dimension), PostgresBucketExpr(bucket, "cf")))
	}
	return strings.Join(lines, "\n")
}

// SupportedFilterDimensionNames returns sorted filterable dimension names from
// the CRM registry plus bucket dimensions.
func SupportedFilterDimensionNames() []string {
	seen := make(map[string]struct{})
	var out []string
	for _, field := range PromotedFields {
		if _, ok := seen[field.Column]; ok {
			continue
		}
		seen[field.Column] = struct{}{}
		out = append(out, field.Column)
	}
	for _, bucket := range BucketDimensions {
		if _, ok := seen[bucket.Dimension]; ok {
			continue
		}
		seen[bucket.Dimension] = struct{}{}
		out = append(out, bucket.Dimension)
	}
	sort.Strings(out)
	return out
}

// SupportedSummarizeDimensionNames returns groupable CRM dimensions.
func SupportedSummarizeDimensionNames() []string {
	seen := make(map[string]struct{})
	var out []string
	for _, field := range PromotedFields {
		if field.Kind != KindCategorical && field.Kind != KindBoolean {
			continue
		}
		if _, ok := seen[field.Column]; ok {
			continue
		}
		seen[field.Column] = struct{}{}
		out = append(out, field.Column)
	}
	for _, bucket := range BucketDimensions {
		if _, ok := seen[bucket.Dimension]; ok {
			continue
		}
		seen[bucket.Dimension] = struct{}{}
		out = append(out, bucket.Dimension)
	}
	sort.Strings(out)
	return out
}

// PostgresDimensionAllowListCSV returns quoted dimension names for SQL IN lists.
func PostgresDimensionAllowListCSV(dimensions []string) string {
	quoted := make([]string, 0, len(dimensions))
	for _, dim := range dimensions {
		quoted = append(quoted, quoteSQLString(dim))
	}
	return strings.Join(quoted, ", ")
}

// IsExcludedFilterDimension reports whether a requested dimension must be rejected.
func IsExcludedFilterDimension(dimension string) bool {
	normalized := strings.ToLower(strings.TrimSpace(dimension))
	for _, excluded := range ExcludedFilterDimensions {
		if normalized == excluded {
			return true
		}
	}
	for _, name := range ExcludedCRMFieldNames {
		if normalized == strings.ToLower(name) {
			return true
		}
	}
	switch normalized {
	case "account_id", "account_website", "opportunity_name", "ownerid", "website", "name", "nextstep":
		return true
	}
	return false
}
