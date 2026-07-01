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

// PromotedField maps a governed call_facts column to one or more Salesforce
// field API names on Account or Opportunity context objects.
type PromotedField struct {
	Column    string
	SFDCNames []string
	Scope     ObjectScope
	Kind      ValueKind
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

// PromotedFields is the canonical business-safe CRM field registry shared by
// SQLite, Postgres, and MCP capability surfaces.
var PromotedFields = []PromotedField{
	// Account categorical / boolean
	{Column: "account_customer_segment_type", SFDCNames: []string{"Customer_Segment_Type__c"}, Scope: ScopeAccount, Kind: KindCategorical},
	{Column: "account_icp_fit_on_rev_range", SFDCNames: []string{"ICP_Fit_on_Rev_Range__c"}, Scope: ScopeAccount, Kind: KindCategorical},
	{Column: "account_icp", SFDCNames: []string{"ICP__c"}, Scope: ScopeAccount, Kind: KindCategorical},
	{Column: "account_value_realization_focus", SFDCNames: []string{"Value_Realization_Focus_Account__c"}, Scope: ScopeAccount, Kind: KindCategorical},
	{Column: "account_bdr_sourced", SFDCNames: []string{"BDR_Sourced__c"}, Scope: ScopeAccount, Kind: KindBoolean},
	{Column: "account_target_account_list_status", SFDCNames: []string{"Target_Account_List_NB_Status__c"}, Scope: ScopeAccount, Kind: KindCategorical},
	{Column: "account_ecommerce_application", SFDCNames: []string{"E_Commerce_Application__c"}, Scope: ScopeAccount, Kind: KindCategorical},
	{Column: "account_billing_terms", SFDCNames: []string{"Billing_Terms__c"}, Scope: ScopeAccount, Kind: KindCategorical},
	{Column: "account_marketing_list", SFDCNames: []string{"Marketing_List__c"}, Scope: ScopeAccount, Kind: KindCategorical},
	{Column: "account_ownership", SFDCNames: []string{"Ownership"}, Scope: ScopeAccount, Kind: KindCategorical},
	{Column: "account_rating", SFDCNames: []string{"Rating"}, Scope: ScopeAccount, Kind: KindCategorical},
	{Column: "account_vertical", SFDCNames: []string{"Vertical__c"}, Scope: ScopeAccount, Kind: KindCategorical},
	{Column: "account_profile_fit_6sense", SFDCNames: []string{"accountProfileFit6sense__c"}, Scope: ScopeAccount, Kind: KindCategorical},

	// Account numeric
	{Column: "account_annual_revenue", SFDCNames: []string{"AnnualRevenue"}, Scope: ScopeAccount, Kind: KindNumeric},
	{Column: "account_employee_count", SFDCNames: []string{"NumberOfEmployees"}, Scope: ScopeAccount, Kind: KindNumeric},
	{Column: "account_total_account_value", SFDCNames: []string{"Total_Account_Value__c"}, Scope: ScopeAccount, Kind: KindNumeric},
	{Column: "account_current_arr_usd", SFDCNames: []string{"Current_ARR_USD_from_SaaSO__c"}, Scope: ScopeAccount, Kind: KindNumeric},
	{Column: "account_intent_score_6sense", SFDCNames: []string{"accountIntentScore6sense__c"}, Scope: ScopeAccount, Kind: KindNumeric},

	// Account dates
	{Column: "account_created_date", SFDCNames: []string{"CreatedDate"}, Scope: ScopeAccount, Kind: KindDate},
	{Column: "account_tal_added_date", SFDCNames: []string{"TAL_Added_Date__c"}, Scope: ScopeAccount, Kind: KindDate},
	{Column: "account_prospect_active_date", SFDCNames: []string{"Prospect_Actve_Date__c"}, Scope: ScopeAccount, Kind: KindDate},
	{Column: "account_prospect_active_end_date", SFDCNames: []string{"Prospect_Active_End_Date__c"}, Scope: ScopeAccount, Kind: KindDate},
	{Column: "account_sqa_date", SFDCNames: []string{"SQA_Date__c"}, Scope: ScopeAccount, Kind: KindDate},
	{Column: "account_sqo_date", SFDCNames: []string{"SQO_Date__c"}, Scope: ScopeAccount, Kind: KindDate},
	{Column: "account_intro_call_set_date", SFDCNames: []string{"Introductory_Call_Set_Date__c"}, Scope: ScopeAccount, Kind: KindDate},
	{Column: "account_discovery_call_completed_date", SFDCNames: []string{"Discovery_Call_Completed_Date__c"}, Scope: ScopeAccount, Kind: KindDate},
	{Column: "account_renewal_date", SFDCNames: []string{"Renewal_Date__c"}, Scope: ScopeAccount, Kind: KindDate},
	{Column: "account_customer_active_end_date", SFDCNames: []string{"Customer_Active_End_Date__c"}, Scope: ScopeAccount, Kind: KindDate},

	// Opportunity categorical / boolean
	{Column: "opportunity_channel_type", SFDCNames: []string{"Channel_Type__c"}, Scope: ScopeOpportunity, Kind: KindCategorical},
	{Column: "opportunity_forecast_category_ae", SFDCNames: []string{"Forecast_Category_AE__c"}, Scope: ScopeOpportunity, Kind: KindCategorical},
	{Column: "opportunity_ecommerce_application", SFDCNames: []string{"E_Commerce_Application__c"}, Scope: ScopeOpportunity, Kind: KindCategorical},
	{Column: "opportunity_sql_primary_source", SFDCNames: []string{"SQL_Primary_Source__c"}, Scope: ScopeOpportunity, Kind: KindCategorical},
	{Column: "opportunity_currency_iso_code", SFDCNames: []string{"CurrencyIsoCode"}, Scope: ScopeOpportunity, Kind: KindCategorical},
	{Column: "opportunity_is_deleted", SFDCNames: []string{"IsDeleted"}, Scope: ScopeOpportunity, Kind: KindBoolean},

	// Opportunity numeric
	{Column: "opportunity_one_year_arr_invoiced", SFDCNames: []string{"One_Year_ARR_Invoiced_Formula__c"}, Scope: ScopeOpportunity, Kind: KindNumeric},
	{Column: "opportunity_expansion_bookings", SFDCNames: []string{"Expansion_Bookings__c"}, Scope: ScopeOpportunity, Kind: KindNumeric},
	{Column: "opportunity_age", SFDCNames: []string{"Opportunity_Age__c"}, Scope: ScopeOpportunity, Kind: KindNumeric},
	{Column: "opportunity_one_time_setup_fees", SFDCNames: []string{"One_Time_Setup_Fees__c"}, Scope: ScopeOpportunity, Kind: KindNumeric},
	{Column: "opportunity_one_year_upsell", SFDCNames: []string{"One_Year_Upsell__c"}, Scope: ScopeOpportunity, Kind: KindNumeric},
	{Column: "opportunity_one_year_arr_uplift", SFDCNames: []string{"One_Year_ARR_Uplift__c"}, Scope: ScopeOpportunity, Kind: KindNumeric},
	{Column: "opportunity_one_year_arr_new_business", SFDCNames: []string{"One_Year_ARR_New_Business__c"}, Scope: ScopeOpportunity, Kind: KindNumeric},

	// Opportunity dates
	{Column: "opportunity_close_date", SFDCNames: []string{"CloseDate"}, Scope: ScopeOpportunity, Kind: KindDate},
	{Column: "opportunity_created_date", SFDCNames: []string{"CreatedDate"}, Scope: ScopeOpportunity, Kind: KindDate},
	{Column: "opportunity_mql_date", SFDCNames: []string{"MQL_Date__c"}, Scope: ScopeOpportunity, Kind: KindDate},
	{Column: "opportunity_sqo_date", SFDCNames: []string{"SQO_Date__c"}, Scope: ScopeOpportunity, Kind: KindDate},
	{Column: "opportunity_sql_date", SFDCNames: []string{"SQL_Date__c"}, Scope: ScopeOpportunity, Kind: KindDate},
	{Column: "opportunity_nurture_follow_up_date", SFDCNames: []string{"Nurture_Follow_Up_Date__c"}, Scope: ScopeOpportunity, Kind: KindDate},
}

// BucketDimensions lists stable group-by dimensions for numeric/date promoted
// columns. opportunity_amount and opportunity_probability already exist on
// call_facts and receive bucket surfaces here without re-promotion.
var BucketDimensions = []BucketDimension{
	{Dimension: "account_annual_revenue_bucket", SourceColumn: "account_annual_revenue", Kind: BucketRevenueUSD},
	{Dimension: "account_employee_count_bucket", SourceColumn: "account_employee_count", Kind: BucketEmployeeCount},
	{Dimension: "account_total_account_value_bucket", SourceColumn: "account_total_account_value", Kind: BucketRevenueUSD},
	{Dimension: "account_current_arr_usd_bucket", SourceColumn: "account_current_arr_usd", Kind: BucketRevenueUSD},
	{Dimension: "account_intent_score_6sense_bucket", SourceColumn: "account_intent_score_6sense", Kind: BucketGenericNumeric},
	{Dimension: "account_created_month", SourceColumn: "account_created_date", Kind: BucketDateMonth},
	{Dimension: "account_created_quarter", SourceColumn: "account_created_date", Kind: BucketDateQuarter},
	{Dimension: "account_tal_added_month", SourceColumn: "account_tal_added_date", Kind: BucketDateMonth},
	{Dimension: "account_tal_added_quarter", SourceColumn: "account_tal_added_date", Kind: BucketDateQuarter},
	{Dimension: "account_prospect_active_month", SourceColumn: "account_prospect_active_date", Kind: BucketDateMonth},
	{Dimension: "account_prospect_active_quarter", SourceColumn: "account_prospect_active_date", Kind: BucketDateQuarter},
	{Dimension: "account_prospect_active_end_month", SourceColumn: "account_prospect_active_end_date", Kind: BucketDateMonth},
	{Dimension: "account_prospect_active_end_quarter", SourceColumn: "account_prospect_active_end_date", Kind: BucketDateQuarter},
	{Dimension: "account_sqa_month", SourceColumn: "account_sqa_date", Kind: BucketDateMonth},
	{Dimension: "account_sqa_quarter", SourceColumn: "account_sqa_date", Kind: BucketDateQuarter},
	{Dimension: "account_sqo_month", SourceColumn: "account_sqo_date", Kind: BucketDateMonth},
	{Dimension: "account_sqo_quarter", SourceColumn: "account_sqo_date", Kind: BucketDateQuarter},
	{Dimension: "account_intro_call_set_month", SourceColumn: "account_intro_call_set_date", Kind: BucketDateMonth},
	{Dimension: "account_intro_call_set_quarter", SourceColumn: "account_intro_call_set_date", Kind: BucketDateQuarter},
	{Dimension: "account_discovery_call_completed_month", SourceColumn: "account_discovery_call_completed_date", Kind: BucketDateMonth},
	{Dimension: "account_discovery_call_completed_quarter", SourceColumn: "account_discovery_call_completed_date", Kind: BucketDateQuarter},
	{Dimension: "account_renewal_month", SourceColumn: "account_renewal_date", Kind: BucketDateMonth},
	{Dimension: "account_renewal_quarter", SourceColumn: "account_renewal_date", Kind: BucketDateQuarter},
	{Dimension: "account_customer_active_end_month", SourceColumn: "account_customer_active_end_date", Kind: BucketDateMonth},
	{Dimension: "account_customer_active_end_quarter", SourceColumn: "account_customer_active_end_date", Kind: BucketDateQuarter},
	{Dimension: "opportunity_amount_bucket", SourceColumn: "opportunity_amount", Kind: BucketRevenueUSD},
	{Dimension: "opportunity_probability_bucket", SourceColumn: "opportunity_probability", Kind: BucketProbability},
	{Dimension: "opportunity_one_year_arr_invoiced_bucket", SourceColumn: "opportunity_one_year_arr_invoiced", Kind: BucketRevenueUSD},
	{Dimension: "opportunity_expansion_bookings_bucket", SourceColumn: "opportunity_expansion_bookings", Kind: BucketRevenueUSD},
	{Dimension: "opportunity_age_bucket", SourceColumn: "opportunity_age", Kind: BucketGenericNumeric},
	{Dimension: "opportunity_one_time_setup_fees_bucket", SourceColumn: "opportunity_one_time_setup_fees", Kind: BucketRevenueUSD},
	{Dimension: "opportunity_one_year_upsell_bucket", SourceColumn: "opportunity_one_year_upsell", Kind: BucketRevenueUSD},
	{Dimension: "opportunity_one_year_arr_uplift_bucket", SourceColumn: "opportunity_one_year_arr_uplift", Kind: BucketRevenueUSD},
	{Dimension: "opportunity_one_year_arr_new_business_bucket", SourceColumn: "opportunity_one_year_arr_new_business", Kind: BucketRevenueUSD},
	{Dimension: "opportunity_close_month", SourceColumn: "opportunity_close_date", Kind: BucketDateMonth},
	{Dimension: "opportunity_close_quarter", SourceColumn: "opportunity_close_date", Kind: BucketDateQuarter},
	{Dimension: "opportunity_created_month", SourceColumn: "opportunity_created_date", Kind: BucketDateMonth},
	{Dimension: "opportunity_created_quarter", SourceColumn: "opportunity_created_date", Kind: BucketDateQuarter},
	{Dimension: "opportunity_mql_month", SourceColumn: "opportunity_mql_date", Kind: BucketDateMonth},
	{Dimension: "opportunity_mql_quarter", SourceColumn: "opportunity_mql_date", Kind: BucketDateQuarter},
	{Dimension: "opportunity_sqo_month", SourceColumn: "opportunity_sqo_date", Kind: BucketDateMonth},
	{Dimension: "opportunity_sqo_quarter", SourceColumn: "opportunity_sqo_date", Kind: BucketDateQuarter},
	{Dimension: "opportunity_sql_month", SourceColumn: "opportunity_sql_date", Kind: BucketDateMonth},
	{Dimension: "opportunity_sql_quarter", SourceColumn: "opportunity_sql_date", Kind: BucketDateQuarter},
	{Dimension: "opportunity_nurture_follow_up_month", SourceColumn: "opportunity_nurture_follow_up_date", Kind: BucketDateMonth},
	{Dimension: "opportunity_nurture_follow_up_quarter", SourceColumn: "opportunity_nurture_follow_up_date", Kind: BucketDateQuarter},
}

// ExcludedSFDCFieldNames must never be advertised as analyst dimensions.
var ExcludedSFDCFieldNames = []string{
	"OwnerId", "SAM__c", "AE__c", "BDR__c",
	"Name", "Website",
	"Marketing_Notes__c", "CSM_Success_Metrics__c", "CSM_General_Notes__c",
	"cirrusadv__Created_by_Cirrus_Insight__c",
	"NextStep", "ISSUE__c", "IMPACT_IMPORTANCE__c", "OTHERS_IMPACTED__c", "RESULTS__c",
	"What_We_Heard_Document__c", "SQL_Source_Notes__c",
	"Referred_By_Account__c", "Referral_SAM__c", "BDR_R__c", "Current_SI_or_Agency__c",
}

// ExcludedFilterDimensions are raw/identifying CRM-adjacent dimensions that
// must be rejected even if present in cached context. account_name and
// crm_object_id keep their existing special-case filter handlers.
var ExcludedFilterDimensions = []string{
	"account_website", "opportunity_name",
	"owner_id", "account_owner_id", "sam__c", "ae__c", "bdr__c",
	"marketing_notes", "next_step", "sql_source_notes",
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
