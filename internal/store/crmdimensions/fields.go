package crmdimensions

// ObjectScope identifies which CRM object a promoted field is sourced from.
type ObjectScope string

const (
	ScopeAccount     ObjectScope = "Account"
	ScopeOpportunity ObjectScope = "Opportunity"
)

// ValueKind classifies how a promoted field is exposed in business analysis.
type ValueKind int

const (
	KindCategorical ValueKind = iota
	KindBoolean
	KindNumeric
	KindDate
)

// PromotedField maps a governed call_facts column to one or more CRM field API
// names on Account or Opportunity context objects.
type PromotedField struct {
	Column        string
	CRMFieldNames []string
	Scope         ObjectScope
	Kind          ValueKind
}

// BucketDimension is a stable, business-friendly grouping surface for a
// promoted numeric or date column. Raw high-cardinality values are never
// groupable directly.
type BucketDimension struct {
	Dimension    string
	SourceColumn string
	Kind         BucketKind
}

// BucketKind selects the bucket/month/quarter expression family.
type BucketKind string

const (
	BucketRevenueUSD     BucketKind = "revenue_usd"
	BucketEmployeeCount  BucketKind = "employee_count"
	BucketProbability    BucketKind = "probability"
	BucketGenericNumeric BucketKind = "generic_numeric"
	BucketDateMonth      BucketKind = "date_month"
	BucketDateQuarter    BucketKind = "date_quarter"
)

// PromotedFields is the canonical business-safe standard-field registry shared
// by SQLite, Postgres, and MCP capability surfaces. Field-name values are
// Salesforce-compatible compatibility defaults; deployment-specific fields belong
// in reviewed profiles or future profile-backed dimensions.
var PromotedFields = []PromotedField{
	// Account categorical
	{Column: "account_ownership", CRMFieldNames: []string{"Ownership"}, Scope: ScopeAccount, Kind: KindCategorical},
	{Column: "account_rating", CRMFieldNames: []string{"Rating"}, Scope: ScopeAccount, Kind: KindCategorical},

	// Account numeric
	{Column: "account_annual_revenue", CRMFieldNames: []string{"AnnualRevenue"}, Scope: ScopeAccount, Kind: KindNumeric},
	{Column: "account_employee_count", CRMFieldNames: []string{"NumberOfEmployees"}, Scope: ScopeAccount, Kind: KindNumeric},

	// Account dates
	{Column: "account_created_date", CRMFieldNames: []string{"CreatedDate"}, Scope: ScopeAccount, Kind: KindDate},

	// Opportunity categorical / boolean
	{Column: "opportunity_currency_iso_code", CRMFieldNames: []string{"CurrencyIsoCode"}, Scope: ScopeOpportunity, Kind: KindCategorical},
	{Column: "opportunity_is_deleted", CRMFieldNames: []string{"IsDeleted"}, Scope: ScopeOpportunity, Kind: KindBoolean},

	// Opportunity dates
	{Column: "opportunity_close_date", CRMFieldNames: []string{"CloseDate"}, Scope: ScopeOpportunity, Kind: KindDate},
	{Column: "opportunity_created_date", CRMFieldNames: []string{"CreatedDate"}, Scope: ScopeOpportunity, Kind: KindDate},
}

// BucketDimensions lists stable group-by dimensions for numeric/date promoted
// columns. opportunity_amount and opportunity_probability already exist on
// call_facts and receive bucket surfaces here without re-promotion.
var BucketDimensions = []BucketDimension{
	{Dimension: "account_annual_revenue_bucket", SourceColumn: "account_annual_revenue", Kind: BucketRevenueUSD},
	{Dimension: "account_employee_count_bucket", SourceColumn: "account_employee_count", Kind: BucketEmployeeCount},
	{Dimension: "account_created_month", SourceColumn: "account_created_date", Kind: BucketDateMonth},
	{Dimension: "account_created_quarter", SourceColumn: "account_created_date", Kind: BucketDateQuarter},
	{Dimension: "opportunity_amount_bucket", SourceColumn: "opportunity_amount", Kind: BucketRevenueUSD},
	{Dimension: "opportunity_probability_bucket", SourceColumn: "opportunity_probability", Kind: BucketProbability},
	{Dimension: "opportunity_close_month", SourceColumn: "opportunity_close_date", Kind: BucketDateMonth},
	{Dimension: "opportunity_close_quarter", SourceColumn: "opportunity_close_date", Kind: BucketDateQuarter},
	{Dimension: "opportunity_created_month", SourceColumn: "opportunity_created_date", Kind: BucketDateMonth},
	{Dimension: "opportunity_created_quarter", SourceColumn: "opportunity_created_date", Kind: BucketDateQuarter},
}

// ExcludedCRMFieldNames must never be advertised as analyst dimensions.
var ExcludedCRMFieldNames = []string{
	"OwnerId",
	"Name", "Website",
	"Description", "NextStep",
}

// ExcludedFilterDimensions are raw/identifying CRM-adjacent dimensions that
// must be rejected even if present in cached context. account_name and
// crm_object_id keep their existing special-case filter handlers.
var ExcludedFilterDimensions = []string{
	"account_website", "opportunity_name",
	"owner_id", "account_owner_id", "next_step",
}

func promotedByColumn() map[string]PromotedField {
	out := make(map[string]PromotedField, len(PromotedFields))
	for _, field := range PromotedFields {
		out[field.Column] = field
	}
	return out
}

var promotedColumnIndex = promotedByColumn()

// LookupPromotedField returns the registry entry for a call_facts column name.
func LookupPromotedField(column string) (PromotedField, bool) {
	field, ok := promotedColumnIndex[column]
	return field, ok
}

// AllPromotedColumns returns sorted call_facts column names from the registry.
func AllPromotedColumns() []string {
	out := make([]string, 0, len(PromotedFields))
	for _, field := range PromotedFields {
		out = append(out, field.Column)
	}
	return out
}
