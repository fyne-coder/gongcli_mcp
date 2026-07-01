package crmdimensions_test

import (
	"strings"
	"testing"

	"github.com/fyne-coder/gongcli_mcp/internal/store/crmdimensions"
)

func TestAccountRatingRegistered(t *testing.T) {
	t.Parallel()

	field, ok := crmdimensions.LookupPromotedField("account_rating")
	if !ok {
		t.Fatal("expected account_rating in registry")
	}
	if field.SFDCNames[0] != "Rating" {
		t.Fatalf("unexpected SFDC mapping: %+v", field.SFDCNames)
	}
	if field.Kind != crmdimensions.KindCategorical {
		t.Fatalf("expected categorical kind, got %v", field.Kind)
	}
}

func TestExcludedDimensionsRejected(t *testing.T) {
	t.Parallel()

	for _, excluded := range []string{"owner_id", "website", "next_step"} {
		if !crmdimensions.IsExcludedFilterDimension(excluded) {
			t.Fatalf("expected %q to be excluded", excluded)
		}
	}
}

func TestSupportedFilterDimensionsIncludeAccountRating(t *testing.T) {
	t.Parallel()

	seen := false
	for _, dim := range crmdimensions.SupportedFilterDimensionNames() {
		if dim == "account_rating" {
			seen = true
			break
		}
	}
	if !seen {
		t.Fatalf("account_rating missing from supported filters: %v", crmdimensions.SupportedFilterDimensionNames())
	}
}

func TestPostgresFilterDimensionCSVsCoverSupportedDimensions(t *testing.T) {
	t.Parallel()

	allowed := splitPostgresDimensionCSV(crmdimensions.PostgresCRMFilterDimensionAllowListCSV())
	typed := splitPostgresDimensionCSV(crmdimensions.PostgresCRMStringEqualsDimensionsCSV())
	for dim := range splitPostgresDimensionCSV(crmdimensions.PostgresCRMNumericFilterDimensionsCSV()) {
		typed[dim] = true
	}
	for dim := range splitPostgresDimensionCSV(crmdimensions.PostgresCRMDateFilterDimensionsCSV()) {
		typed[dim] = true
	}
	for dim := range splitPostgresDimensionCSV(crmdimensions.PostgresCRMBooleanFilterDimensionsCSV()) {
		typed[dim] = true
	}

	for dim := range allowed {
		if !typed[dim] {
			t.Fatalf("supported Postgres filter dimension %q is not covered by a typed matcher family", dim)
		}
	}
	for dim := range typed {
		if !allowed[dim] {
			t.Fatalf("typed Postgres filter dimension %q is missing from supported filter dimensions", dim)
		}
	}
}

func splitPostgresDimensionCSV(csv string) map[string]bool {
	dims := make(map[string]bool)
	for _, part := range strings.Split(csv, ",") {
		dim := strings.Trim(strings.TrimSpace(part), "'")
		if dim != "" {
			dims[dim] = true
		}
	}
	return dims
}

func TestBucketDimensionsIncludeRepresentativeNumericAndDate(t *testing.T) {
	t.Parallel()

	want := map[string]bool{
		"account_annual_revenue_bucket": false,
		"opportunity_close_month":       false,
	}
	for _, bucket := range crmdimensions.BucketDimensions {
		if _, ok := want[bucket.Dimension]; ok {
			want[bucket.Dimension] = true
		}
	}
	for dim, ok := range want {
		if !ok {
			t.Fatalf("missing bucket dimension %q", dim)
		}
	}
}

func TestPromotedNumericAndDateFieldsHaveGroupBuckets(t *testing.T) {
	t.Parallel()

	bucketsBySource := make(map[string]int)
	for _, bucket := range crmdimensions.BucketDimensions {
		bucketsBySource[bucket.SourceColumn]++
	}
	for _, field := range crmdimensions.PromotedFields {
		switch field.Kind {
		case crmdimensions.KindNumeric:
			if bucketsBySource[field.Column] == 0 {
				t.Fatalf("numeric field %s has no grouping bucket", field.Column)
			}
		case crmdimensions.KindDate:
			if bucketsBySource[field.Column] < 2 {
				t.Fatalf("date field %s should have month and quarter grouping buckets", field.Column)
			}
		}
	}
}

func TestSQLiteMigrationSQLIncludesAccountRating(t *testing.T) {
	t.Parallel()

	if !strings.Contains(crmdimensions.SQLiteCallFactsViewMigrationSQL, "Rating") {
		t.Fatal("sqlite migration missing Rating promotion")
	}
	if !strings.Contains(crmdimensions.SQLiteCallFactsViewMigrationSQL, "account_rating") {
		t.Fatal("sqlite migration missing account_rating column")
	}
}

func TestPostgresPromotionSQLIncludesAccountRating(t *testing.T) {
	t.Parallel()

	sqlText := crmdimensions.PostgresCRMPromotionLinesForScope(crmdimensions.ScopeAccount)
	if !strings.Contains(sqlText, "Rating") {
		t.Fatalf("postgres promotion SQL missing Rating: %s", sqlText)
	}
}

func TestPostgresPromotionSQLAcceptsDecimalNumericValues(t *testing.T) {
	t.Parallel()

	sqlText := crmdimensions.PostgresCRMPromotionLinesForScope(crmdimensions.ScopeAccount)
	if !strings.Contains(sqlText, "::numeric::bigint") {
		t.Fatalf("postgres numeric promotion should cast decimal CRM values through numeric: %s", sqlText)
	}
}
