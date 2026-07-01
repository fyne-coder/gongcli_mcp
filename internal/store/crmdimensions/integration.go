package crmdimensions

import (
	"sort"
	"strings"
)

// FilterKindForDimension maps a backed dimension to the sqlite business
// analysis filter kind string: string, numeric, boolean.
func FilterKindForDimension(dimension string) (string, bool) {
	if IsExcludedFilterDimension(dimension) {
		return "", false
	}
	for _, bucket := range BucketDimensions {
		if bucket.Dimension == dimension {
			return "string", true
		}
	}
	field, ok := LookupPromotedField(dimension)
	if !ok {
		return "", false
	}
	switch field.Kind {
	case KindNumeric:
		return "numeric", true
	case KindBoolean:
		return "boolean", true
	case KindDate:
		return "date", true
	default:
		return "string", true
	}
}

// FilterExpr returns the SQL expression used for dimension_filters on call_facts.
func FilterExpr(dimension, callFactsAlias string) (string, bool) {
	if IsExcludedFilterDimension(dimension) {
		return "", false
	}
	for _, bucket := range BucketDimensions {
		if bucket.Dimension == dimension {
			return SQLiteBucketExpr(bucket, callFactsAlias), true
		}
	}
	field, ok := LookupPromotedField(dimension)
	if !ok {
		return "", false
	}
	if field.Kind == KindNumeric && (dimension == "opportunity_amount" || dimension == "opportunity_probability") {
		// Legacy text columns: compare via cast for numeric operators.
		return "CAST(COALESCE(NULLIF(REPLACE(REPLACE(TRIM(" + callFactsAlias + "." + dimension + "), ',', ''), '$', ''), ''), '0') AS INTEGER)", true
	}
	return callFactsAlias + "." + field.Column, true
}

// SummarizeExpr returns the SQL expression for summarize/dimension_counts grouping.
func SummarizeExpr(dimension, callFactsAlias string) (string, bool) {
	if IsExcludedFilterDimension(dimension) {
		return "", false
	}
	for _, bucket := range BucketDimensions {
		if bucket.Dimension == dimension {
			return SQLiteBucketExpr(bucket, callFactsAlias), true
		}
	}
	field, ok := LookupPromotedField(dimension)
	if !ok {
		return "", false
	}
	if field.Kind != KindCategorical && field.Kind != KindBoolean {
		return "", false
	}
	return callFactsAlias + "." + field.Column, true
}

// MergeBackedFilterDimensions appends registry-backed dimensions to an existing
// candidate list without duplicates.
func MergeBackedFilterDimensions(candidates []string) []string {
	seen := make(map[string]struct{}, len(candidates))
	out := make([]string, 0, len(candidates)+len(PromotedFields))
	for _, dim := range candidates {
		if _, ok := seen[dim]; ok {
			continue
		}
		seen[dim] = struct{}{}
		out = append(out, dim)
	}
	for _, dim := range SupportedFilterDimensionNames() {
		if _, ok := seen[dim]; ok {
			continue
		}
		seen[dim] = struct{}{}
		out = append(out, dim)
	}
	sort.Strings(out)
	return out
}

// AliasesForDimension returns known aliases for a canonical dimension name.
func AliasesForDimension(dimension string) []string {
	return nil
}

// BuildAliasMap returns lowercase alias -> canonical dimension mappings.
func BuildAliasMap() map[string]string {
	out := make(map[string]string)
	for _, dim := range SupportedFilterDimensionNames() {
		out[strings.ToLower(dim)] = dim
		for _, alias := range AliasesForDimension(dim) {
			out[strings.ToLower(alias)] = dim
		}
	}
	return out
}
