package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// Phase B-1 read-time speaker attribution status/source/confidence values.
// These constants are part of the documented MCP contract for
// evidence.call_drilldown; downstream callers compare on the literal
// strings, so do not rename without updating the facade contract.
const (
	AttributionStatusAvailable               = "available"
	AttributionStatusParticipantTitleMissing = "participant_matched_title_missing"
	AttributionStatusSpeakerUnmatched        = "speaker_unmatched"
	AttributionStatusSpeakerAmbiguous        = "speaker_ambiguous"
	AttributionSourceCallParties             = "call_parties"
	AttributionSourceGongParty               = "gong_party"
	AttributionSourceUnmatched               = "unmatched"
	AttributionConfidenceExactSpeakerID      = "exact_speaker_id"
	AttributionConfidenceAmbiguous           = "ambiguous"
	AttributionConfidenceUnmatched           = "unmatched"
	// SpeakerRole values are the safe, low-cardinality buyer-vs-rep
	// signal the gap-followup slice surfaces alongside speaker
	// attribution. Roles are derived from the cached Gong party
	// `affiliation` field (Internal / External). When the party data
	// does not give us an answer we emit `unknown` plus a status that
	// names the reason so callers preserve the uncertainty.
	SpeakerRoleInternal                 = "internal"
	SpeakerRoleExternal                 = "external"
	SpeakerRoleUnknown                  = "unknown"
	SpeakerRoleStatusAvailable          = "available"
	SpeakerRoleStatusAffiliationMissing = "affiliation_missing"
	SpeakerRoleStatusSpeakerUnmatched   = "speaker_unmatched"
	SpeakerRoleStatusSpeakerAmbiguous   = "speaker_ambiguous"
)

const (
	defaultBusinessAnalysisLimit       = 25
	maxBusinessAnalysisLimit           = 1000
	maxBusinessAnalysisFTSQueryLength  = 160
	maxBusinessAnalysisFTSQueryTerms   = 12
	maxBusinessAnalysisFTSQueryTermLen = 48
)

type BusinessAnalysisFilter struct {
	TitleQuery            string `json:"title_query,omitempty"`
	Query                 string `json:"query,omitempty"`
	FromDate              string `json:"from_date,omitempty"`
	ToDate                string `json:"to_date,omitempty"`
	Quarter               string `json:"quarter,omitempty"`
	LifecycleBucket       string `json:"lifecycle_bucket,omitempty"`
	Scope                 string `json:"scope,omitempty"`
	System                string `json:"system,omitempty"`
	Direction             string `json:"direction,omitempty"`
	TranscriptStatus      string `json:"transcript_status,omitempty"`
	Industry              string `json:"industry,omitempty"`
	AccountQuery          string `json:"account_query,omitempty"`
	OpportunityStage      string `json:"opportunity_stage,omitempty"`
	CRMObjectType         string `json:"crm_object_type,omitempty"`
	CRMObjectID           string `json:"crm_object_id,omitempty"`
	ParticipantTitleQuery string `json:"participant_title_query,omitempty"`
	Limit                 int    `json:"limit,omitempty"`
}

type BusinessAnalysisCallSearchParams struct {
	Filter BusinessAnalysisFilter
	Limit  int
}

type BusinessAnalysisCallSearchResult struct {
	Filter  BusinessAnalysisFilter        `json:"filter"`
	Summary BusinessAnalysisCohortSummary `json:"summary"`
	Rows    []BusinessAnalysisCallRow     `json:"rows"`
}

type BusinessAnalysisCohortSummary struct {
	CallCount                 int64   `json:"call_count"`
	TranscriptCount           int64   `json:"transcript_count"`
	MissingTranscriptCount    int64   `json:"missing_transcript_count"`
	TranscriptCoverageRate    float64 `json:"transcript_coverage_rate"`
	AccountIndustryCount      int64   `json:"account_industry_count"`
	OpportunityStageCount     int64   `json:"opportunity_stage_count"`
	OpportunityCallCount      int64   `json:"opportunity_call_count"`
	AccountCallCount          int64   `json:"account_call_count"`
	ExternalCallCount         int64   `json:"external_call_count"`
	ParticipantCallCount      int64   `json:"participant_call_count"`
	ParticipantTitleCallCount int64   `json:"participant_title_call_count"`
	ParticipantTitleRate      float64 `json:"participant_title_rate"`
	EarliestCallAt            string  `json:"earliest_call_at,omitempty"`
	LatestCallAt              string  `json:"latest_call_at,omitempty"`
	TotalDurationSeconds      int64   `json:"total_duration_seconds"`
	AverageDurationSeconds    float64 `json:"average_duration_seconds"`
	CRMOutcomeCoverageHint    string  `json:"crm_outcome_coverage_hint,omitempty"`
	ParticipantCoverageHint   string  `json:"participant_coverage_hint,omitempty"`
	IndustryCoverageHint      string  `json:"industry_coverage_hint,omitempty"`
	CacheDerivedNotLLMClaims  bool    `json:"cache_derived_not_llm_claims"`
}

type BusinessAnalysisCallRow struct {
	CallID            string `json:"call_id,omitempty"`
	Title             string `json:"title,omitempty"`
	StartedAt         string `json:"started_at,omitempty"`
	CallDate          string `json:"call_date,omitempty"`
	CallMonth         string `json:"call_month,omitempty"`
	DurationSeconds   int64  `json:"duration_seconds,omitempty"`
	LifecycleBucket   string `json:"lifecycle_bucket,omitempty"`
	Scope             string `json:"scope,omitempty"`
	System            string `json:"system,omitempty"`
	Direction         string `json:"direction,omitempty"`
	TranscriptStatus  string `json:"transcript_status,omitempty"`
	AccountIndustry   string `json:"account_industry,omitempty"`
	OpportunityStage  string `json:"opportunity_stage,omitempty"`
	OpportunityType   string `json:"opportunity_type,omitempty"`
	ForecastCategory  string `json:"forecast_category,omitempty"`
	OpportunityCount  int64  `json:"opportunity_count,omitempty"`
	AccountCount      int64  `json:"account_count,omitempty"`
	ParticipantStatus string `json:"participant_status,omitempty"`
	PersonTitleStatus string `json:"person_title_status,omitempty"`
	PersonTitleSource string `json:"person_title_source,omitempty"`
}

type BusinessAnalysisEvidenceSearchParams struct {
	Filter BusinessAnalysisFilter
	Query  string
	Limit  int
	// BroadDiscovery enables a seedless evidence sample for broad theme
	// discovery: when true and Query/Filter.Query are blank, the store
	// returns a deterministic, bounded transcript-segment sample within
	// the cohort filter instead of requiring a full-text query. Only
	// discover_themes_in_cohort should set this; all other evidence and
	// quote searches must keep query-required semantics.
	BroadDiscovery bool
}

type BusinessAnalysisEvidenceRow struct {
	CallID                 string `json:"call_id,omitempty"`
	Title                  string `json:"title,omitempty"`
	StartedAt              string `json:"started_at,omitempty"`
	CallDate               string `json:"call_date,omitempty"`
	CallMonth              string `json:"call_month,omitempty"`
	LifecycleBucket        string `json:"lifecycle_bucket,omitempty"`
	AccountIndustry        string `json:"account_industry,omitempty"`
	AccountName            string `json:"account_name,omitempty"`
	OpportunityName        string `json:"opportunity_name,omitempty"`
	OpportunityStage       string `json:"opportunity_stage,omitempty"`
	OpportunityType        string `json:"opportunity_type,omitempty"`
	OpportunityProbability string `json:"opportunity_probability,omitempty"`
	OpportunityCloseDate   string `json:"opportunity_close_date,omitempty"`
	ParticipantStatus      string `json:"participant_status,omitempty"`
	PersonTitleStatus      string `json:"person_title_status,omitempty"`
	PersonTitleSource      string `json:"person_title_source,omitempty"`
	SegmentIndex           int    `json:"segment_index,omitempty"`
	StartMS                int64  `json:"start_ms,omitempty"`
	EndMS                  int64  `json:"end_ms,omitempty"`
	Snippet                string `json:"snippet,omitempty"`
	ContextExcerpt         string `json:"context_excerpt,omitempty"`
	// SpeakerID is the cached transcript-segment speaker key. The SQLite
	// quote-search path populates it from `transcript_segments.speaker_id`
	// so the MCP layer can match the segment against cached Gong party
	// affiliation and emit SpeakerRole/SpeakerRoleStatus on the
	// quote/business-workbench facade. Postgres SECURITY DEFINER functions
	// resolve the safe role/status values in SQL and intentionally do not
	// expose this hidden speaker key through MCP responses.
	SpeakerID         string `json:"-"`
	SpeakerRole       string `json:"-"`
	SpeakerRoleStatus string `json:"-"`
}

type BusinessAnalysisDimensionSummaryParams struct {
	Filter     BusinessAnalysisFilter
	Dimension  string
	ThemeQuery string
	Limit      int
}

type BusinessAnalysisDimensionRow struct {
	Dimension              string  `json:"dimension"`
	Value                  string  `json:"value"`
	CallCount              int64   `json:"call_count"`
	TranscriptCount        int64   `json:"transcript_count"`
	MissingTranscriptCount int64   `json:"missing_transcript_count"`
	TranscriptCoverageRate float64 `json:"transcript_coverage_rate"`
	OpportunityCallCount   int64   `json:"opportunity_call_count"`
	AccountCallCount       int64   `json:"account_call_count"`
	ExternalCallCount      int64   `json:"external_call_count"`
	LatestCallAt           string  `json:"latest_call_at,omitempty"`
}

// AIHighlightListParams is the bounded input contract for listing typed Gong
// AI highlights. CallIDs contains already-resolved raw call IDs and is capped
// by the caller; Limit is honored as an upper bound on returned rows.
type AIHighlightListParams struct {
	CallIDs []string
	Limit   int
}

// AIHighlightRow is one redacted, typed Gong AI highlight row. The Postgres
// call_ai_highlights read model writes only these typed columns; raw
// highlight JSON is intentionally not exposed.
type AIHighlightRow struct {
	CallID         string `json:"call_id"`
	CallRef        string `json:"call_ref,omitempty"`
	HighlightIndex int    `json:"highlight_index"`
	HighlightType  string `json:"highlight_type"`
	HighlightText  string `json:"highlight_text"`
	SourcePath     string `json:"source_path,omitempty"`
	UpdatedAt      string `json:"updated_at,omitempty"`
}

// ListAIHighlights returns no rows for the SQLite cache: the typed
// call_ai_highlights read model only exists in the Postgres serving DB. This
// stub keeps the Store interface uniform without changing SQLite schema or
// behavior.
func (s *Store) ListAIHighlights(_ context.Context, _ AIHighlightListParams) ([]AIHighlightRow, error) {
	return nil, nil
}

// CallDrilldownEvidenceParams is the bounded input contract for the exact
// call-scoped transcript evidence path used by evidence.call_drilldown. Query
// is optional; when blank, the operation returns no transcript rows so callers
// receive only AI condensed evidence and can decide whether to ask for a
// theme.
type CallDrilldownEvidenceParams struct {
	CallID string
	Query  string
	Limit  int
}

// CallDrilldownEvidenceRow is one bounded transcript excerpt scoped to a
// specific call. The shape stays minimal: the call-drilldown facade joins
// account/opportunity coverage from the call detail rather than denormalizing
// it onto every excerpt.
//
// The attribution-related fields (PersonTitleStatus, PersonTitleSource,
// AttributionSource, AttributionConfidence, PersonTitle) describe how the
// row's speaker_id was matched against Gong-party metadata in the cache.
// Phase B-1 supports exact Gong-party-id matching only; CRM Contact/Lead
// attribution is explicitly future work and the row never carries inferred
// titles or persona buckets.
type CallDrilldownEvidenceRow struct {
	CallID                string `json:"-"`
	SegmentIndex          int    `json:"segment_index"`
	SpeakerID             string `json:"speaker_id,omitempty"`
	StartMS               int64  `json:"start_ms"`
	EndMS                 int64  `json:"end_ms"`
	Snippet               string `json:"snippet,omitempty"`
	ContextExcerpt        string `json:"context_excerpt,omitempty"`
	PersonTitleStatus     string `json:"-"`
	PersonTitleSource     string `json:"-"`
	AttributionSource     string `json:"-"`
	AttributionConfidence string `json:"-"`
	PersonTitle           string `json:"-"`
	// SpeakerRole is the buyer-vs-rep signal derived from cached Gong
	// party `affiliation` data: SpeakerRoleInternal / SpeakerRoleExternal /
	// SpeakerRoleUnknown. SpeakerRoleStatus names *why* a role is unknown
	// so the facade layer can preserve uncertainty rather than guessing.
	SpeakerRole       string `json:"-"`
	SpeakerRoleStatus string `json:"-"`
}

const (
	defaultCallDrilldownTranscriptLimit = 10
	maxCallDrilldownTranscriptLimit     = 25
)

// CallDrilldownEvidence returns bounded transcript excerpts scoped to a single
// call. Callers must provide an already-resolved CallID; the facade layer is
// responsible for translating call_ref values and applying suppression and
// blocklist filters before serialization.
func (s *Store) CallDrilldownEvidence(ctx context.Context, params CallDrilldownEvidenceParams) ([]CallDrilldownEvidenceRow, error) {
	callID := strings.TrimSpace(params.CallID)
	if callID == "" {
		return nil, errors.New("call_id is required for call_drilldown evidence")
	}
	limit := boundedLimit(params.Limit, defaultCallDrilldownTranscriptLimit, maxCallDrilldownTranscriptLimit)
	queryText := strings.TrimSpace(params.Query)
	if queryText == "" {
		return nil, nil
	}
	queryMatch, err := businessAnalysisFTSQuery(queryText, "query")
	if err != nil {
		return nil, err
	}
	if queryMatch == "" {
		return nil, nil
	}
	rows, err := s.db.QueryContext(ctx, `
SELECT ts.call_id,
       ts.segment_index,
       ts.speaker_id,
       ts.start_ms,
       ts.end_ms,
       snippet(transcript_segments_fts, 0, '', '', '...', 18) AS snippet,
       substr(COALESCE((
               SELECT group_concat(context_text, ' ')
                 FROM (
                       SELECT ctx.text AS context_text
                         FROM transcript_segments ctx
                        WHERE ctx.call_id = ts.call_id
                          AND ctx.segment_index BETWEEN ts.segment_index - 1 AND ts.segment_index + 1
                        ORDER BY ctx.segment_index
                 )
       ), ''), 1, 800) AS context_excerpt
  FROM transcript_segments_fts
  JOIN transcript_segments ts ON ts.id = transcript_segments_fts.rowid
 WHERE transcript_segments_fts MATCH ?
   AND ts.call_id = ?
 ORDER BY bm25(transcript_segments_fts), ts.segment_index
 LIMIT ?`, queryMatch, callID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]CallDrilldownEvidenceRow, 0)
	for rows.Next() {
		var row CallDrilldownEvidenceRow
		if err := rows.Scan(&row.CallID, &row.SegmentIndex, &row.SpeakerID, &row.StartMS, &row.EndMS, &row.Snippet, &row.ContextExcerpt); err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(out) == 0 {
		return out, nil
	}
	parties, err := s.loadCallPartyAttribution(ctx, callID)
	if err != nil {
		return nil, err
	}
	for i := range out {
		applyCallPartyAttribution(&out[i], parties)
	}
	return out, nil
}

// callPartyAttribution describes one Gong party loaded from the cached call's
// raw_json. Only the attribution-relevant fields (speaker keys + raw title)
// are retained; downstream callers must not re-derive persona/title from
// transcript text.
type callPartyAttribution struct {
	SpeakerKeys map[string]struct{}
	Title       string
	// Role normalizes Gong's party `affiliation` field into the safe
	// internal / external / "" tri-state. Callers that match a speaker
	// to one of these party records can promote the role onto the
	// transcript row; an empty Role means the underlying data did not
	// give us an answer.
	Role string
}

// loadCallPartyAttribution returns the parsed party records for one call.
// Both `$.parties` and `$.metaData.parties` are scanned and the union is
// returned. Callers use the returned slice once per drilldown query and
// resolve transcript speaker_id values against the slice in Go.
func (s *Store) loadCallPartyAttribution(ctx context.Context, callID string) ([]callPartyAttribution, error) {
	if strings.TrimSpace(callID) == "" {
		return nil, nil
	}
	var raw []byte
	err := s.db.QueryRowContext(ctx, `SELECT raw_json FROM calls WHERE call_id = ? LIMIT 1`, callID).Scan(&raw)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	if len(raw) == 0 {
		return nil, nil
	}
	var doc struct {
		Parties  []json.RawMessage `json:"parties"`
		MetaData *struct {
			Parties []json.RawMessage `json:"parties"`
		} `json:"metaData"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, nil
	}
	parties := append([]json.RawMessage(nil), doc.Parties...)
	if doc.MetaData != nil {
		parties = append(parties, doc.MetaData.Parties...)
	}
	out := make([]callPartyAttribution, 0, len(parties))
	for _, p := range parties {
		entry, ok := decodeCallPartyAttribution(p)
		if !ok {
			continue
		}
		out = append(out, entry)
	}
	return out, nil
}

func decodeCallPartyAttribution(raw json.RawMessage) (callPartyAttribution, bool) {
	if len(raw) == 0 {
		return callPartyAttribution{}, false
	}
	var party map[string]any
	if err := json.Unmarshal(raw, &party); err != nil {
		return callPartyAttribution{}, false
	}
	keys := make(map[string]struct{}, 3)
	for _, candidate := range []string{"speakerId", "speaker_id", "id"} {
		if v, ok := party[candidate].(string); ok {
			trimmed := strings.TrimSpace(v)
			if trimmed != "" {
				keys[trimmed] = struct{}{}
			}
		}
	}
	if len(keys) == 0 {
		return callPartyAttribution{}, false
	}
	title := ""
	for _, candidate := range []string{"title", "jobTitle", "job_title"} {
		if v, ok := party[candidate].(string); ok {
			trimmed := strings.TrimSpace(v)
			if trimmed != "" {
				title = trimmed
				break
			}
		}
	}
	role := ""
	for _, candidate := range []string{"affiliation", "Affiliation", "partyType", "party_type"} {
		if v, ok := party[candidate].(string); ok {
			switch strings.ToLower(strings.TrimSpace(v)) {
			case "internal":
				role = SpeakerRoleInternal
			case "external":
				role = SpeakerRoleExternal
			case "unknown", "":
				// leave empty so the apply step emits SpeakerRoleStatusAffiliationMissing.
			}
			if role != "" {
				break
			}
		}
	}
	if role == "" {
		for _, candidate := range []string{"isInternal", "is_internal"} {
			v, ok := party[candidate].(bool)
			if !ok {
				continue
			}
			// Some payloads use boolean isInternal flags. Map true->internal,
			// false->external; no-op when the field is absent.
			if v {
				role = SpeakerRoleInternal
			} else {
				role = SpeakerRoleExternal
			}
			break
		}
	}
	return callPartyAttribution{SpeakerKeys: keys, Title: title, Role: role}, true
}

// applyCallPartyAttribution sets PersonTitleStatus / PersonTitleSource /
// AttributionSource / AttributionConfidence / PersonTitle on the row based
// on exact Gong-party speaker_id matching only. Phase B-1 deliberately does
// not consult CRM Contact/Lead records, transcript text, or any heuristic
// title extraction; callers must surface explicit limitation copy.
func applyCallPartyAttribution(row *CallDrilldownEvidenceRow, parties []callPartyAttribution) {
	speakerID := strings.TrimSpace(row.SpeakerID)
	if speakerID == "" {
		row.PersonTitleStatus = AttributionStatusSpeakerUnmatched
		row.PersonTitleSource = ""
		row.AttributionSource = AttributionSourceUnmatched
		row.AttributionConfidence = AttributionConfidenceUnmatched
		row.SpeakerRole = SpeakerRoleUnknown
		row.SpeakerRoleStatus = SpeakerRoleStatusSpeakerUnmatched
		return
	}
	matches := make([]callPartyAttribution, 0, 2)
	for _, p := range parties {
		if _, ok := p.SpeakerKeys[speakerID]; ok {
			matches = append(matches, p)
		}
	}
	switch len(matches) {
	case 0:
		row.PersonTitleStatus = AttributionStatusSpeakerUnmatched
		row.PersonTitleSource = ""
		row.AttributionSource = AttributionSourceUnmatched
		row.AttributionConfidence = AttributionConfidenceUnmatched
		row.SpeakerRole = SpeakerRoleUnknown
		row.SpeakerRoleStatus = SpeakerRoleStatusSpeakerUnmatched
	case 1:
		row.AttributionSource = AttributionSourceGongParty
		row.AttributionConfidence = AttributionConfidenceExactSpeakerID
		row.PersonTitleSource = AttributionSourceCallParties
		if matches[0].Title != "" {
			row.PersonTitleStatus = AttributionStatusAvailable
			row.PersonTitle = matches[0].Title
		} else {
			row.PersonTitleStatus = AttributionStatusParticipantTitleMissing
		}
		if matches[0].Role != "" {
			row.SpeakerRole = matches[0].Role
			row.SpeakerRoleStatus = SpeakerRoleStatusAvailable
		} else {
			row.SpeakerRole = SpeakerRoleUnknown
			row.SpeakerRoleStatus = SpeakerRoleStatusAffiliationMissing
		}
	default:
		row.PersonTitleStatus = AttributionStatusSpeakerAmbiguous
		row.PersonTitleSource = AttributionSourceCallParties
		row.AttributionSource = AttributionSourceGongParty
		row.AttributionConfidence = AttributionConfidenceAmbiguous
		// Role is ambiguous in this case: even if all matched parties
		// share an affiliation we surface the speaker_ambiguous status
		// so callers do not collapse an ambiguous match into a
		// confident role.
		role := ""
		conflict := false
		for _, m := range matches {
			if m.Role == "" {
				continue
			}
			if role == "" {
				role = m.Role
				continue
			}
			if m.Role != role {
				conflict = true
				break
			}
		}
		if conflict || role == "" {
			row.SpeakerRole = SpeakerRoleUnknown
		} else {
			row.SpeakerRole = role
		}
		row.SpeakerRoleStatus = SpeakerRoleStatusSpeakerAmbiguous
	}
}

func (s *Store) SearchBusinessAnalysisCalls(ctx context.Context, params BusinessAnalysisCallSearchParams) (*BusinessAnalysisCallSearchResult, error) {
	filter, err := normalizeBusinessAnalysisFilter(params.Filter)
	if err != nil {
		return nil, err
	}
	limit := boundedLimit(firstPositiveInt(params.Limit, filter.Limit), defaultBusinessAnalysisLimit, maxBusinessAnalysisLimit)
	filter.Limit = limit

	from, where, args, err := businessAnalysisCallFromWhere(filter, true)
	if err != nil {
		return nil, err
	}
	summary, err := s.businessAnalysisCohortSummary(ctx, from, where, args)
	if err != nil {
		return nil, err
	}

	query := `
SELECT cf.call_id,
       cf.title,
       cf.started_at,
       cf.call_date,
       cf.call_month,
       cf.duration_seconds,
       cf.lifecycle_bucket,
       cf.scope,
       cf.system,
       cf.direction,
       cf.transcript_status,
       cf.account_industry,
       cf.opportunity_stage,
       cf.opportunity_type,
       cf.opportunity_forecast_category,
       cf.opportunity_count,
       cf.account_count,
       CASE WHEN c.parties_count > 0 THEN 'present' ELSE 'missing_from_cache' END AS participant_status,
       ` + businessAnalysisPersonTitleStatusSQL("c") + ` AS person_title_status,
       ` + businessAnalysisPersonTitleSourceSQL("c") + ` AS person_title_source
` + from
	if len(where) > 0 {
		query += `
 WHERE ` + strings.Join(where, ` AND `)
	}
	query += `
 ORDER BY cf.started_at DESC, cf.call_id
 LIMIT ?`
	rowArgs := append(append([]any{}, args...), limit)
	rows, err := s.db.QueryContext(ctx, query, rowArgs...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]BusinessAnalysisCallRow, 0)
	for rows.Next() {
		var row BusinessAnalysisCallRow
		if err := rows.Scan(
			&row.CallID,
			&row.Title,
			&row.StartedAt,
			&row.CallDate,
			&row.CallMonth,
			&row.DurationSeconds,
			&row.LifecycleBucket,
			&row.Scope,
			&row.System,
			&row.Direction,
			&row.TranscriptStatus,
			&row.AccountIndustry,
			&row.OpportunityStage,
			&row.OpportunityType,
			&row.ForecastCategory,
			&row.OpportunityCount,
			&row.AccountCount,
			&row.ParticipantStatus,
			&row.PersonTitleStatus,
			&row.PersonTitleSource,
		); err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	return &BusinessAnalysisCallSearchResult{
		Filter:  filter,
		Summary: summary,
		Rows:    out,
	}, nil
}

func (s *Store) SearchBusinessAnalysisEvidence(ctx context.Context, params BusinessAnalysisEvidenceSearchParams) ([]BusinessAnalysisEvidenceRow, error) {
	filter, err := normalizeBusinessAnalysisFilter(params.Filter)
	if err != nil {
		return nil, err
	}
	limit := boundedLimit(firstPositiveInt(params.Limit, filter.Limit), defaultBusinessAnalysisLimit, maxBusinessAnalysisLimit)
	queryText := strings.TrimSpace(firstNonEmptyString(params.Query, filter.Query))
	queryMatch, err := businessAnalysisFTSQuery(queryText, "query")
	if err != nil {
		return nil, err
	}
	seedless := queryMatch == "" && params.BroadDiscovery
	if queryMatch == "" && !seedless {
		return nil, errors.New("query is required for business-analysis evidence searches")
	}

	_, where, args, err := businessAnalysisCallFromWhere(filter, true)
	if err != nil {
		return nil, err
	}
	var selectSnippet, orderBy, from string
	if seedless {
		selectSnippet = `substr(ts.text, 1, 400) AS snippet`
		orderBy = `cf.started_at DESC, ts.call_id, ts.segment_index`
		from = `
  FROM transcript_segments ts
  JOIN call_facts cf
    ON cf.call_id = ts.call_id
  JOIN calls c
    ON c.call_id = cf.call_id`
	} else {
		from = `
  FROM transcript_segments_fts
  JOIN transcript_segments ts
    ON ts.id = transcript_segments_fts.rowid
  JOIN call_facts cf
    ON cf.call_id = ts.call_id
  JOIN calls c
    ON c.call_id = cf.call_id`
		where = append([]string{`transcript_segments_fts MATCH ?`}, where...)
		args = append([]any{queryMatch}, args...)
		selectSnippet = `snippet(transcript_segments_fts, 0, '', '', '...', 18) AS snippet`
		orderBy = `bm25(transcript_segments_fts), cf.started_at DESC, ts.call_id, ts.segment_index`
	}

	sql := `
SELECT cf.call_id,
       cf.title,
       cf.started_at,
       cf.call_date,
       cf.call_month,
       cf.lifecycle_bucket,
       cf.account_industry,
       COALESCE((SELECT TRIM(o.object_name) FROM call_context_objects o WHERE o.call_id = cf.call_id AND o.object_type = 'Account' AND TRIM(o.object_name) <> '' ORDER BY o.object_key LIMIT 1), '') AS account_name,
       COALESCE((SELECT TRIM(o.object_name) FROM call_context_objects o WHERE o.call_id = cf.call_id AND o.object_type = 'Opportunity' AND TRIM(o.object_name) <> '' ORDER BY o.object_key LIMIT 1), '') AS opportunity_name,
       cf.opportunity_stage,
       cf.opportunity_type,
       cf.opportunity_probability,
       COALESCE((SELECT TRIM(f.field_value_text) FROM call_context_fields f JOIN call_context_objects o ON o.call_id = f.call_id AND o.object_key = f.object_key WHERE f.call_id = cf.call_id AND o.object_type = 'Opportunity' AND f.field_name IN ('CloseDate', 'closeDate', 'Close_Date__c') AND TRIM(f.field_value_text) <> '' ORDER BY f.id LIMIT 1), '') AS opportunity_close_date,
       CASE WHEN c.parties_count > 0 THEN 'present' ELSE 'missing_from_cache' END AS participant_status,
       ` + businessAnalysisPersonTitleStatusSQL("c") + ` AS person_title_status,
       ` + businessAnalysisPersonTitleSourceSQL("c") + ` AS person_title_source,
       ts.speaker_id,
       ts.segment_index,
       ts.start_ms,
       ts.end_ms,
       ` + selectSnippet + `,
       substr(COALESCE((
	       SELECT group_concat(context_text, ' ')
	         FROM (
		       SELECT ctx.text AS context_text
		         FROM transcript_segments ctx
		        WHERE ctx.call_id = ts.call_id
		          AND ctx.segment_index BETWEEN ts.segment_index - 1 AND ts.segment_index + 1
		        ORDER BY ctx.segment_index
	         )
       ), ''), 1, 800) AS context_excerpt
` + from
	if len(where) > 0 {
		sql += `
 WHERE ` + strings.Join(where, ` AND `)
	}
	sql += `
 ORDER BY ` + orderBy + `
 LIMIT ?`
	args = append(args, limit)

	rows, err := s.db.QueryContext(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]BusinessAnalysisEvidenceRow, 0)
	for rows.Next() {
		var row BusinessAnalysisEvidenceRow
		if err := rows.Scan(
			&row.CallID,
			&row.Title,
			&row.StartedAt,
			&row.CallDate,
			&row.CallMonth,
			&row.LifecycleBucket,
			&row.AccountIndustry,
			&row.AccountName,
			&row.OpportunityName,
			&row.OpportunityStage,
			&row.OpportunityType,
			&row.OpportunityProbability,
			&row.OpportunityCloseDate,
			&row.ParticipantStatus,
			&row.PersonTitleStatus,
			&row.PersonTitleSource,
			&row.SpeakerID,
			&row.SegmentIndex,
			&row.StartMS,
			&row.EndMS,
			&row.Snippet,
			&row.ContextExcerpt,
		); err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if err := s.applyBusinessAnalysisSpeakerRoles(ctx, out); err != nil {
		return nil, err
	}
	return out, nil
}

// applyBusinessAnalysisSpeakerRoles annotates each evidence row with the
// safe buyer-vs-rep SpeakerRole and SpeakerRoleStatus derived from the
// cached Gong party `affiliation` field. Resolution mirrors the
// CallDrilldownEvidence path: speaker_id values are matched against the
// per-call party records loaded from `calls.raw_json`. Rows without a
// speaker_id, with no matching party, or with multiple ambiguous matches
// stay at SpeakerRole=unknown with the corresponding status so callers do
// not silently collapse uncertainty into a guess.
func (s *Store) applyBusinessAnalysisSpeakerRoles(ctx context.Context, rows []BusinessAnalysisEvidenceRow) error {
	if len(rows) == 0 {
		return nil
	}
	cache := make(map[string][]callPartyAttribution)
	for i := range rows {
		callID := strings.TrimSpace(rows[i].CallID)
		if callID == "" {
			rows[i].SpeakerRole = SpeakerRoleUnknown
			rows[i].SpeakerRoleStatus = SpeakerRoleStatusSpeakerUnmatched
			continue
		}
		parties, ok := cache[callID]
		if !ok {
			loaded, err := s.loadCallPartyAttribution(ctx, callID)
			if err != nil {
				return err
			}
			parties = loaded
			cache[callID] = parties
		}
		role, status := resolveSpeakerRole(strings.TrimSpace(rows[i].SpeakerID), parties)
		rows[i].SpeakerRole = role
		rows[i].SpeakerRoleStatus = status
	}
	return nil
}

// resolveSpeakerRole resolves a transcript-segment speaker_id against the
// loaded callPartyAttribution slice and returns the safe SpeakerRole /
// SpeakerRoleStatus pair for the row. The buyer-vs-rep signal stays at
// "unknown" when the speaker is unmatched, ambiguous, or matches a party
// without affiliation data.
func resolveSpeakerRole(speakerID string, parties []callPartyAttribution) (string, string) {
	if speakerID == "" {
		return SpeakerRoleUnknown, SpeakerRoleStatusSpeakerUnmatched
	}
	matches := make([]callPartyAttribution, 0, 1)
	for _, party := range parties {
		if _, ok := party.SpeakerKeys[speakerID]; ok {
			matches = append(matches, party)
		}
	}
	switch len(matches) {
	case 0:
		return SpeakerRoleUnknown, SpeakerRoleStatusSpeakerUnmatched
	case 1:
		role := strings.TrimSpace(matches[0].Role)
		if role == SpeakerRoleInternal || role == SpeakerRoleExternal {
			return role, SpeakerRoleStatusAvailable
		}
		return SpeakerRoleUnknown, SpeakerRoleStatusAffiliationMissing
	}
	role := ""
	for _, party := range matches {
		candidate := strings.TrimSpace(party.Role)
		if candidate == "" {
			continue
		}
		if role == "" {
			role = candidate
			continue
		}
		if role != candidate {
			return SpeakerRoleUnknown, SpeakerRoleStatusSpeakerAmbiguous
		}
	}
	if role == "" {
		return SpeakerRoleUnknown, SpeakerRoleStatusSpeakerAmbiguous
	}
	return role, SpeakerRoleStatusSpeakerAmbiguous
}

func (s *Store) applyTranscriptAttributionSpeakerRoles(ctx context.Context, rows []TranscriptAttributionSearchResult) error {
	if len(rows) == 0 {
		return nil
	}
	cache := make(map[string][]callPartyAttribution)
	for i := range rows {
		callID := strings.TrimSpace(rows[i].CallID)
		if callID == "" {
			rows[i].SpeakerRole = SpeakerRoleUnknown
			rows[i].SpeakerRoleStatus = SpeakerRoleStatusSpeakerUnmatched
			continue
		}
		parties, ok := cache[callID]
		if !ok {
			loaded, err := s.loadCallPartyAttribution(ctx, callID)
			if err != nil {
				return err
			}
			parties = loaded
			cache[callID] = parties
		}
		role, status := resolveSpeakerRole(strings.TrimSpace(rows[i].SpeakerID), parties)
		rows[i].SpeakerRole = role
		rows[i].SpeakerRoleStatus = status
	}
	return nil
}

func (s *Store) SummarizeBusinessAnalysisDimension(ctx context.Context, params BusinessAnalysisDimensionSummaryParams) ([]BusinessAnalysisDimensionRow, error) {
	filter, err := normalizeBusinessAnalysisFilter(params.Filter)
	if err != nil {
		return nil, err
	}
	if themeQuery := strings.TrimSpace(params.ThemeQuery); themeQuery != "" {
		var err error
		from, where, args, err := businessAnalysisCallFromWhere(filter, true)
		if err != nil {
			return nil, err
		}
		where, args, err = businessAnalysisAppendTranscriptQueryFilter(where, args, themeQuery, "theme_query")
		if err != nil {
			return nil, err
		}
		return s.summarizeBusinessAnalysisDimension(ctx, filter, params.Dimension, params.Limit, from, where, args)
	}
	from, where, args, err := businessAnalysisCallFromWhere(filter, true)
	if err != nil {
		return nil, err
	}
	return s.summarizeBusinessAnalysisDimension(ctx, filter, params.Dimension, params.Limit, from, where, args)
}

func (s *Store) summarizeBusinessAnalysisDimension(ctx context.Context, filter BusinessAnalysisFilter, requestedDimension string, requestedLimit int, from string, where []string, args []any) ([]BusinessAnalysisDimensionRow, error) {
	limit := boundedLimit(firstPositiveInt(requestedLimit, filter.Limit), defaultBusinessAnalysisLimit, maxBusinessAnalysisLimit)
	dimension, expr, err := businessAnalysisDimensionExpr(requestedDimension)
	if err != nil {
		return nil, err
	}
	sql := `
WITH filtered AS (
	SELECT cf.*,
	       ` + expr + ` AS dimension_value
` + from
	if len(where) > 0 {
		sql += `
 WHERE ` + strings.Join(where, ` AND `)
	}
	sql += `
)
SELECT ? AS dimension,
       COALESCE(NULLIF(TRIM(dimension_value), ''), '<blank>') AS value,
       COUNT(*) AS call_count,
       SUM(CASE WHEN transcript_present = 1 THEN 1 ELSE 0 END) AS transcript_count,
       SUM(CASE WHEN transcript_present = 0 THEN 1 ELSE 0 END) AS missing_transcript_count,
       SUM(CASE WHEN opportunity_count > 0 THEN 1 ELSE 0 END) AS opportunity_call_count,
       SUM(CASE WHEN account_count > 0 THEN 1 ELSE 0 END) AS account_call_count,
       SUM(CASE WHEN scope = 'External' THEN 1 ELSE 0 END) AS external_call_count,
       COALESCE(MAX(started_at), '') AS latest_call_at
  FROM filtered
 GROUP BY value
 ORDER BY call_count DESC, value
 LIMIT ?`
	args = append(args, dimension, limit)

	rows, err := s.db.QueryContext(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]BusinessAnalysisDimensionRow, 0)
	for rows.Next() {
		var row BusinessAnalysisDimensionRow
		if err := rows.Scan(
			&row.Dimension,
			&row.Value,
			&row.CallCount,
			&row.TranscriptCount,
			&row.MissingTranscriptCount,
			&row.OpportunityCallCount,
			&row.AccountCallCount,
			&row.ExternalCallCount,
			&row.LatestCallAt,
		); err != nil {
			return nil, err
		}
		row.TranscriptCoverageRate = rate(row.TranscriptCount, row.CallCount)
		out = append(out, row)
	}
	return out, rows.Err()
}

func (s *Store) businessAnalysisCohortSummary(ctx context.Context, from string, where []string, args []any) (BusinessAnalysisCohortSummary, error) {
	query := `
SELECT COUNT(*) AS call_count,
       COALESCE(SUM(CASE WHEN cf.transcript_present = 1 THEN 1 ELSE 0 END), 0) AS transcript_count,
       COALESCE(SUM(CASE WHEN cf.transcript_present = 0 THEN 1 ELSE 0 END), 0) AS missing_transcript_count,
       COALESCE(SUM(CASE WHEN TRIM(cf.account_industry) <> '' THEN 1 ELSE 0 END), 0) AS account_industry_count,
       COALESCE(SUM(CASE WHEN TRIM(cf.opportunity_stage) <> '' THEN 1 ELSE 0 END), 0) AS opportunity_stage_count,
       COALESCE(SUM(CASE WHEN cf.opportunity_count > 0 THEN 1 ELSE 0 END), 0) AS opportunity_call_count,
       COALESCE(SUM(CASE WHEN cf.account_count > 0 THEN 1 ELSE 0 END), 0) AS account_call_count,
       COALESCE(SUM(CASE WHEN cf.scope = 'External' THEN 1 ELSE 0 END), 0) AS external_call_count,
       COALESCE(SUM(CASE WHEN c.parties_count > 0 THEN 1 ELSE 0 END), 0) AS participant_call_count,
       COALESCE(SUM(CASE WHEN ` + businessAnalysisPartyTitleCountSQL("c") + ` > 0 THEN 1 ELSE 0 END), 0) AS participant_title_call_count,
       COALESCE(MIN(cf.started_at), '') AS earliest_call_at,
       COALESCE(MAX(cf.started_at), '') AS latest_call_at,
       COALESCE(SUM(cf.duration_seconds), 0) AS total_duration_seconds,
       COALESCE(AVG(cf.duration_seconds), 0) AS average_duration_seconds
` + from
	if len(where) > 0 {
		query += `
 WHERE ` + strings.Join(where, ` AND `)
	}
	var summary BusinessAnalysisCohortSummary
	if err := s.db.QueryRowContext(ctx, query, args...).Scan(
		&summary.CallCount,
		&summary.TranscriptCount,
		&summary.MissingTranscriptCount,
		&summary.AccountIndustryCount,
		&summary.OpportunityStageCount,
		&summary.OpportunityCallCount,
		&summary.AccountCallCount,
		&summary.ExternalCallCount,
		&summary.ParticipantCallCount,
		&summary.ParticipantTitleCallCount,
		&summary.EarliestCallAt,
		&summary.LatestCallAt,
		&summary.TotalDurationSeconds,
		&summary.AverageDurationSeconds,
	); err != nil {
		return BusinessAnalysisCohortSummary{}, err
	}
	summary.TranscriptCoverageRate = rate(summary.TranscriptCount, summary.CallCount)
	summary.ParticipantTitleRate = rate(summary.ParticipantTitleCallCount, summary.CallCount)
	summary.CRMOutcomeCoverageHint = coverageHint(summary.OpportunityStageCount, summary.CallCount, "opportunity stage")
	summary.ParticipantCoverageHint = coverageHint(summary.ParticipantTitleCallCount, summary.CallCount, "participant title")
	summary.IndustryCoverageHint = coverageHint(summary.AccountIndustryCount, summary.CallCount, "account industry")
	summary.CacheDerivedNotLLMClaims = true
	return summary, nil
}

func businessAnalysisCallFromWhere(filter BusinessAnalysisFilter, includeFilterQuery bool) (string, []string, []any, error) {
	from := `
  FROM call_facts cf
  JOIN calls c
    ON c.call_id = cf.call_id`
	var where []string
	var args []any

	if value := strings.TrimSpace(filter.TitleQuery); value != "" {
		where = append(where, `LOWER(cf.title) LIKE '%' || LOWER(?) || '%'`)
		args = append(args, value)
	}
	if includeFilterQuery {
		var err error
		where, args, err = businessAnalysisAppendTranscriptQueryFilter(where, args, filter.Query, "filter.query")
		if err != nil {
			return "", nil, nil, err
		}
	}
	if value := strings.TrimSpace(filter.FromDate); value != "" {
		where = append(where, `cf.call_date >= ?`)
		args = append(args, value)
	}
	if value := strings.TrimSpace(filter.ToDate); value != "" {
		where = append(where, `cf.call_date <= ?`)
		args = append(args, value)
	}
	if value := strings.TrimSpace(filter.LifecycleBucket); value != "" {
		where = append(where, `cf.lifecycle_bucket = ?`)
		args = append(args, strings.ToLower(value))
	}
	if value := strings.TrimSpace(filter.Scope); value != "" {
		where = append(where, `cf.scope = ?`)
		args = append(args, value)
	}
	if value := strings.TrimSpace(filter.System); value != "" {
		where = append(where, `cf.system = ?`)
		args = append(args, value)
	}
	if value := strings.TrimSpace(filter.Direction); value != "" {
		where = append(where, `cf.direction = ?`)
		args = append(args, value)
	}
	if value := strings.TrimSpace(filter.TranscriptStatus); value != "" && value != "any" {
		where = append(where, `cf.transcript_status = ?`)
		args = append(args, value)
	}
	if value := strings.TrimSpace(filter.Industry); value != "" {
		where = append(where, `LOWER(cf.account_industry) LIKE '%' || LOWER(?) || '%'`)
		args = append(args, value)
	}
	if value := strings.TrimSpace(filter.AccountQuery); value != "" {
		where = append(where, `EXISTS (
			SELECT 1
			  FROM call_context_objects account_o
			 WHERE account_o.call_id = cf.call_id
			   AND account_o.object_type = 'Account'
			   AND LOWER(TRIM(account_o.object_name)) LIKE '%' || LOWER(?) || '%'
		)`)
		args = append(args, value)
	}
	if value := strings.TrimSpace(filter.OpportunityStage); value != "" {
		where = append(where, `LOWER(cf.opportunity_stage) LIKE '%' || LOWER(?) || '%'`)
		args = append(args, value)
	}
	if strings.TrimSpace(filter.CRMObjectType) != "" || strings.TrimSpace(filter.CRMObjectID) != "" {
		subquery := []string{`filter_o.call_id = cf.call_id`}
		if value := strings.TrimSpace(filter.CRMObjectType); value != "" {
			subquery = append(subquery, `filter_o.object_type = ?`)
			args = append(args, value)
		}
		if value := strings.TrimSpace(filter.CRMObjectID); value != "" {
			subquery = append(subquery, `filter_o.object_id = ?`)
			args = append(args, value)
		}
		where = append(where, `EXISTS (SELECT 1 FROM call_context_objects filter_o WHERE `+strings.Join(subquery, ` AND `)+`)`)
	}
	if value := strings.TrimSpace(filter.ParticipantTitleQuery); value != "" {
		where = append(where, businessAnalysisParticipantTitleLikeSQL("c", "?"))
		args = append(args, value, value, value)
	}
	return from, where, args, nil
}

func businessAnalysisAppendTranscriptQueryFilter(where []string, args []any, rawQuery string, field string) ([]string, []any, error) {
	query, err := businessAnalysisFTSQuery(rawQuery, field)
	if err != nil {
		return nil, nil, err
	}
	if query == "" {
		return where, args, nil
	}
	where = append(where, `cf.call_id IN (
		SELECT query_ts.call_id
		  FROM transcript_segments_fts
		  JOIN transcript_segments query_ts
		    ON query_ts.id = transcript_segments_fts.rowid
		 WHERE transcript_segments_fts MATCH ?
	)`)
	args = append(args, query)
	return where, args, nil
}

func businessAnalysisFTSQuery(value string, field string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", nil
	}
	if len(value) > maxBusinessAnalysisFTSQueryLength {
		return "", fmt.Errorf("%s must be %d characters or fewer", field, maxBusinessAnalysisFTSQueryLength)
	}
	var tokens []string
	var token strings.Builder
	flush := func() error {
		if token.Len() == 0 {
			return nil
		}
		text := strings.ToLower(token.String())
		token.Reset()
		if len(text) > maxBusinessAnalysisFTSQueryTermLen {
			return fmt.Errorf("%s terms must be %d characters or fewer", field, maxBusinessAnalysisFTSQueryTermLen)
		}
		tokens = append(tokens, text)
		if len(tokens) > maxBusinessAnalysisFTSQueryTerms {
			return fmt.Errorf("%s must include no more than %d search terms", field, maxBusinessAnalysisFTSQueryTerms)
		}
		return nil
	}
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z':
			token.WriteRune(r)
		case r >= 'A' && r <= 'Z':
			token.WriteRune(r)
		case r >= '0' && r <= '9':
			token.WriteRune(r)
		case r == ' ' || r == '\t' || r == '\n' || r == '\r' || r == '-' || r == '_' || r == '\'' || r == '"' || r == '/' || r == '.' || r == ',' || r == ';' || r == ':':
			if err := flush(); err != nil {
				return "", err
			}
		default:
			return "", fmt.Errorf("%s may contain only letters, numbers, spaces, and simple separators", field)
		}
	}
	if err := flush(); err != nil {
		return "", err
	}
	if len(tokens) == 0 {
		return "", fmt.Errorf("%s must include at least one search term", field)
	}
	quoted := make([]string, 0, len(tokens))
	for _, item := range tokens {
		quoted = append(quoted, `"`+item+`"`)
	}
	return strings.Join(quoted, " "), nil
}

func normalizeBusinessAnalysisFilter(filter BusinessAnalysisFilter) (BusinessAnalysisFilter, error) {
	filter.TitleQuery = strings.ToLower(strings.TrimSpace(filter.TitleQuery))
	filter.Query = strings.TrimSpace(filter.Query)
	filter.FromDate = strings.TrimSpace(filter.FromDate)
	filter.ToDate = strings.TrimSpace(filter.ToDate)
	filter.Quarter = strings.TrimSpace(filter.Quarter)
	filter.LifecycleBucket = strings.TrimSpace(filter.LifecycleBucket)
	filter.Scope = strings.TrimSpace(filter.Scope)
	filter.System = strings.TrimSpace(filter.System)
	filter.Direction = strings.TrimSpace(filter.Direction)
	filter.TranscriptStatus = strings.ToLower(strings.TrimSpace(filter.TranscriptStatus))
	filter.Industry = strings.TrimSpace(filter.Industry)
	filter.AccountQuery = strings.TrimSpace(filter.AccountQuery)
	filter.OpportunityStage = strings.TrimSpace(filter.OpportunityStage)
	filter.CRMObjectType = strings.TrimSpace(filter.CRMObjectType)
	filter.CRMObjectID = strings.TrimSpace(filter.CRMObjectID)
	filter.ParticipantTitleQuery = strings.TrimSpace(filter.ParticipantTitleQuery)

	if filter.Quarter != "" {
		canonical, from, to, err := normalizeBusinessAnalysisQuarter(filter.Quarter)
		if err != nil {
			return BusinessAnalysisFilter{}, err
		}
		filter.Quarter = canonical
		if filter.FromDate == "" {
			filter.FromDate = from
		}
		if filter.ToDate == "" {
			filter.ToDate = to
		}
	}
	var err error
	if filter.FromDate, err = normalizeDateFilter(filter.FromDate, "from_date"); err != nil {
		return BusinessAnalysisFilter{}, err
	}
	if filter.ToDate, err = normalizeDateFilter(filter.ToDate, "to_date"); err != nil {
		return BusinessAnalysisFilter{}, err
	}
	if filter.FromDate != "" && filter.ToDate != "" && filter.FromDate > filter.ToDate {
		return BusinessAnalysisFilter{}, errors.New("from_date must be on or before to_date")
	}
	if filter.LifecycleBucket != "" && !isKnownLifecycleBucket(filter.LifecycleBucket) {
		return BusinessAnalysisFilter{}, fmt.Errorf("unknown lifecycle bucket %q", filter.LifecycleBucket)
	}
	if filter.Scope != "" {
		scope, ok := normalizedScope(filter.Scope)
		if !ok {
			return BusinessAnalysisFilter{}, errors.New("scope must be one of: External, Internal, Unknown")
		}
		filter.Scope = scope
	}
	if filter.TranscriptStatus != "" && filter.TranscriptStatus != "any" {
		status, ok := normalizedTranscriptStatus(filter.TranscriptStatus)
		if !ok {
			return BusinessAnalysisFilter{}, errors.New("transcript_status must be one of: present, missing, any")
		}
		filter.TranscriptStatus = status
	}
	if filter.Limit > 0 {
		filter.Limit = boundedLimit(filter.Limit, defaultBusinessAnalysisLimit, maxBusinessAnalysisLimit)
	}
	return filter, nil
}

func normalizeBusinessAnalysisQuarter(value string) (string, string, string, error) {
	normalized := strings.ToUpper(strings.ReplaceAll(strings.TrimSpace(value), " ", "-"))
	normalized = strings.ReplaceAll(normalized, "_", "-")
	var year, quarter string
	switch {
	case len(normalized) == len("2026-Q1") && normalized[4] == '-' && normalized[5] == 'Q':
		year = normalized[:4]
		quarter = normalized[5:]
	case len(normalized) == len("2026Q1") && normalized[4] == 'Q':
		year = normalized[:4]
		quarter = normalized[4:]
	case len(normalized) == len("Q1-2026") && normalized[0] == 'Q' && normalized[2] == '-':
		quarter = normalized[:2]
		year = normalized[3:]
	default:
		return "", "", "", fmt.Errorf("quarter must look like 2026-Q1")
	}
	if _, err := normalizeDateFilter(year+"-01-01", "quarter year"); err != nil {
		return "", "", "", fmt.Errorf("quarter must include a four-digit year")
	}
	switch quarter {
	case "Q1":
		return year + "-Q1", year + "-01-01", year + "-03-31", nil
	case "Q2":
		return year + "-Q2", year + "-04-01", year + "-06-30", nil
	case "Q3":
		return year + "-Q3", year + "-07-01", year + "-09-30", nil
	case "Q4":
		return year + "-Q4", year + "-10-01", year + "-12-31", nil
	default:
		return "", "", "", fmt.Errorf("quarter must be Q1, Q2, Q3, or Q4")
	}
}

func businessAnalysisDimensionExpr(dimension string) (string, string, error) {
	switch strings.ToLower(strings.TrimSpace(dimension)) {
	case "", "lifecycle", "lifecycle_bucket":
		return "lifecycle", "cf.lifecycle_bucket", nil
	case "industry", "account_industry":
		return "industry", "cf.account_industry", nil
	case "persona", "participant_title", "title":
		return "persona", businessAnalysisPersonaBucketSQL("c", "cf"), nil
	case "opportunity_stage", "stage":
		return "opportunity_stage", "cf.opportunity_stage", nil
	case "opportunity_type":
		return "opportunity_type", "cf.opportunity_type", nil
	case "forecast_category":
		return "forecast_category", "cf.opportunity_forecast_category", nil
	case "scope":
		return "scope", "cf.scope", nil
	case "system":
		return "system", "cf.system", nil
	case "direction":
		return "direction", "cf.direction", nil
	case "transcript_status":
		return "transcript_status", "cf.transcript_status", nil
	case "month", "call_month":
		return "month", "cf.call_month", nil
	case "quarter":
		return "quarter", businessAnalysisQuarterExpr("cf.call_date"), nil
	case "won_lost", "outcome":
		return "won_lost", `CASE
			WHEN LOWER(cf.opportunity_stage) = 'closed won' THEN 'closed_won'
			WHEN LOWER(cf.opportunity_stage) = 'closed lost' THEN 'closed_lost'
			WHEN TRIM(cf.opportunity_stage) <> '' THEN 'open_or_in_progress'
			ELSE 'unknown'
		END`, nil
	case "loss_reason":
		return "loss_reason", businessAnalysisLossReasonBucketSQL(), nil
	default:
		return "", "", fmt.Errorf("unsupported business-analysis dimension %q", dimension)
	}
}

func businessAnalysisQuarterExpr(callDateExpr string) string {
	return `CASE
		WHEN substr(` + callDateExpr + `, 6, 2) IN ('01','02','03') THEN substr(` + callDateExpr + `, 1, 4) || '-Q1'
		WHEN substr(` + callDateExpr + `, 6, 2) IN ('04','05','06') THEN substr(` + callDateExpr + `, 1, 4) || '-Q2'
		WHEN substr(` + callDateExpr + `, 6, 2) IN ('07','08','09') THEN substr(` + callDateExpr + `, 1, 4) || '-Q3'
		WHEN substr(` + callDateExpr + `, 6, 2) IN ('10','11','12') THEN substr(` + callDateExpr + `, 1, 4) || '-Q4'
		ELSE ''
	END`
}

func businessAnalysisOpportunityFieldExpr(fieldNames []string) string {
	quoted := make([]string, 0, len(fieldNames))
	for _, name := range fieldNames {
		quoted = append(quoted, "'"+strings.ReplaceAll(name, "'", "''")+"'")
	}
	return `COALESCE((
		SELECT TRIM(f.field_value_text)
		  FROM call_context_fields f
		  JOIN call_context_objects o
		    ON o.call_id = f.call_id
		   AND o.object_key = f.object_key
		 WHERE f.call_id = cf.call_id
		   AND o.object_type = 'Opportunity'
		   AND f.field_name IN (` + strings.Join(quoted, ",") + `)
		   AND TRIM(f.field_value_text) <> ''
		 ORDER BY f.id
		 LIMIT 1
	), '')`
}

func businessAnalysisPartyTitleCountSQL(callAlias string) string {
	return `COALESCE((SELECT COUNT(1) FROM json_each(` + callAlias + `.raw_json, '$.parties') p WHERE TRIM(COALESCE(json_extract(p.value, '$.title'), json_extract(p.value, '$.jobTitle'), json_extract(p.value, '$.job_title'), '')) <> ''), 0) +
	       COALESCE((SELECT COUNT(1) FROM json_each(` + callAlias + `.raw_json, '$.metaData.parties') p WHERE TRIM(COALESCE(json_extract(p.value, '$.title'), json_extract(p.value, '$.jobTitle'), json_extract(p.value, '$.job_title'), '')) <> ''), 0)`
}

func businessAnalysisPersonTitleStatusSQL(callAlias string) string {
	return `CASE
		WHEN ` + businessAnalysisPartyTitleCountSQL(callAlias) + ` > 0 THEN 'available'
		WHEN EXISTS (SELECT 1 FROM call_context_objects po WHERE po.call_id = ` + callAlias + `.call_id AND po.object_type IN ('Contact', 'Lead')) THEN 'contact_or_lead_present_title_unverified'
		WHEN ` + callAlias + `.parties_count > 0 THEN 'participants_present_check_party_titles'
		ELSE 'missing_from_cache'
	END`
}

func businessAnalysisPersonTitleSourceSQL(callAlias string) string {
	return `CASE
		WHEN ` + businessAnalysisPartyTitleCountSQL(callAlias) + ` > 0 THEN 'call_parties'
		WHEN EXISTS (SELECT 1 FROM call_context_objects po WHERE po.call_id = ` + callAlias + `.call_id AND po.object_type IN ('Contact', 'Lead')) THEN 'contact_or_lead_object'
		ELSE ''
	END`
}

// personaBucketRules is the deterministic, priority-ordered keyword mapping
// from raw participant/Contact/Lead title text to the coarse persona buckets
// surfaced by the persona dimension. The mapping intentionally returns
// coarse roles (`procurement`, `it_security_integration`, ...) rather than
// the original title text so business-analysis output cannot be used to
// enumerate raw titles. Patterns are matched as case-insensitive substrings
// against a normalized title string that has had punctuation collapsed to
// spaces and one leading/trailing space added so short acronyms can be
// matched as whole tokens.
type personaBucketRule struct {
	Bucket   string
	Patterns []string
}

func personaBucketRules() []personaBucketRule {
	return []personaBucketRule{
		{Bucket: "procurement", Patterns: []string{"procurement", "purchasing", "sourcing", "buyer", "category manager"}},
		{Bucket: "supplier_enablement", Patterns: []string{"supplier", "vendor manager", "vendor management", "vendor enablement", "channel manager", "partner manager", "alliances"}},
		{Bucket: "it_security_integration", Patterns: []string{" ciso ", " cio ", " cto ", "chief information", "chief technology", "vp it", "vp of it", "head of it", "it director", "it manager", "infrastructure", "security", "infosec", "integration", "architect", "devops", "platform engineer", "site reliability"}},
		{Bucket: "finance", Patterns: []string{" cfo ", "chief financial", "finance", "controller", "treasur", "accounting"}},
		{Bucket: "operations", Patterns: []string{" coo ", "chief operating", "operations", "supply chain", "logistics", "manufacturing", "production", "fulfillment"}},
		{Bucket: "sales_revenue", Patterns: []string{"sales", "revenue", "account exec", "account manager", " sdr ", " bdr ", " ae ", " csm ", "customer success", "go-to-market", "gtm"}},
		{Bucket: "executive", Patterns: []string{" ceo ", "chief executive", "founder", "president", "chair", "general manager"}},
	}
}

// businessAnalysisPersonaBucketSQL returns a SQLite expression that resolves
// to one of the coarse persona buckets for the call referenced by callAlias
// (joined `calls` row) and callFactsAlias (joined `call_facts` row). Inputs
// are participant titles from `calls.raw_json` (parties / metaData.parties)
// and CRM Contact/Lead Title fields from cached call_context_*. When no
// title matches a bucket pattern but a non-empty title exists the call is
// classified as `other_title_present`; otherwise the expression evaluates
// to an empty string so the dimension summary's existing `<blank>` coverage row is
// emitted.
func businessAnalysisPersonaBucketSQL(callAlias, callFactsAlias string) string {
	// Normalize titles: collapse common punctuation to spaces and pad with
	// leading/trailing spaces so short acronyms can be matched as whole
	// tokens via patterns like ' ceo '.
	normalize := func(expr string) string {
		return "' ' || REPLACE(REPLACE(REPLACE(REPLACE(REPLACE(REPLACE(LOWER(TRIM(" + expr + ")), ',', ' '), '/', ' '), '-', ' '), '.', ' '), '\\t', ' '), '\\n', ' ') || ' '"
	}
	titlesCTE := `
		SELECT ` + normalize("COALESCE(json_extract(p.value, '$.title'), json_extract(p.value, '$.jobTitle'), json_extract(p.value, '$.job_title'), '')") + ` AS t
		  FROM json_each(` + callAlias + `.raw_json, '$.parties') p
		 WHERE json_extract(p.value, '$.title') IS NOT NULL
		    OR json_extract(p.value, '$.jobTitle') IS NOT NULL
		    OR json_extract(p.value, '$.job_title') IS NOT NULL
		UNION ALL
		SELECT ` + normalize("COALESCE(json_extract(p.value, '$.title'), json_extract(p.value, '$.jobTitle'), json_extract(p.value, '$.job_title'), '')") + `
		  FROM json_each(` + callAlias + `.raw_json, '$.metaData.parties') p
		 WHERE json_extract(p.value, '$.title') IS NOT NULL
		    OR json_extract(p.value, '$.jobTitle') IS NOT NULL
		    OR json_extract(p.value, '$.job_title') IS NOT NULL
		UNION ALL
		SELECT ` + normalize("f.field_value_text") + `
		  FROM call_context_fields f
		  JOIN call_context_objects o
		    ON o.call_id = f.call_id
		   AND o.object_key = f.object_key
		 WHERE f.call_id = ` + callFactsAlias + `.call_id
		   AND o.object_type IN ('Contact', 'Lead')
		   AND f.field_name IN ('Title', 'JobTitle', 'Job_Title__c', 'JobTitle__c')
	`
	bucketCases := strings.Builder{}
	for _, rule := range personaBucketRules() {
		predicates := make([]string, 0, len(rule.Patterns))
		for _, pattern := range rule.Patterns {
			predicates = append(predicates, "titles.t LIKE '%"+escapeBucketLike(pattern)+"%'")
		}
		bucketCases.WriteString("\n\t\tWHEN EXISTS (SELECT 1 FROM (")
		bucketCases.WriteString(titlesCTE)
		bucketCases.WriteString(") titles WHERE TRIM(titles.t) <> '' AND (")
		bucketCases.WriteString(strings.Join(predicates, " OR "))
		bucketCases.WriteString(")) THEN '")
		bucketCases.WriteString(rule.Bucket)
		bucketCases.WriteString("'")
	}
	return `CASE` + bucketCases.String() + `
		WHEN EXISTS (SELECT 1 FROM (` + titlesCTE + `) titles WHERE TRIM(titles.t) <> '') THEN 'other_title_present'
		ELSE ''
	END`
}

// LossReasonFieldNames is the deterministic list of Opportunity field names
// that may carry a free-text loss reason for the call's primary opportunity.
// The ordering reflects priority when multiple are populated. The list is the
// single source of truth shared by the SQLite dimension SQL, the Postgres
// gongmcp_business_analysis_loss_reason_bucket function, and the Postgres
// read-model has_loss_reason flag — extending this slice automatically
// extends loss-reason source coverage on both backends.
var LossReasonFieldNames = []string{
	"LossReason",
	"Loss_Reason__c",
	"Closed_Lost_Reason__c",
	"Closed_Lost_Reason_Detail__c",
	"Lost_Reason__c",
	"Reason_Lost__c",
	"OpportunityLossReason",
	"lossReason",
	"loss_reason",
}

// lossReasonBucketRule binds a normalized loss-reason bucket label to the
// case-insensitive substring patterns that map raw CRM loss-reason text into
// it. Patterns are matched against a normalized form (lower-cased, with
// punctuation/underscores collapsed to spaces and one leading/trailing
// space). Rule order is the deterministic priority used when raw text could
// satisfy more than one bucket.
type lossReasonBucketRule struct {
	Bucket   string
	Patterns []string
}

// lossReasonBucketRules returns the priority-ordered keyword mapping from
// raw Opportunity loss-reason text to coarse normalized buckets. The mapping
// intentionally returns coarse labels (`price`, `feature_gap`, ...) rather
// than the original loss-reason string so business-analysis output cannot be
// used to enumerate raw CRM values.
func lossReasonBucketRules() []lossReasonBucketRule {
	return []lossReasonBucketRule{
		{Bucket: "competitor", Patterns: []string{"competitor", "competition", "competitive", "incumbent", "displaced"}},
		{Bucket: "feature_gap", Patterns: []string{"feature gap", "missing feature", "feature missing", "missing functionality", "feature not", "lack of feature", "lacking feature", "product gap", "capability gap"}},
		{Bucket: "budget", Patterns: []string{"budget", "no funding", "no funds", "funding cut", "out of money"}},
		{Bucket: "price", Patterns: []string{"price", "pricing", "too expensive", "cost too high", "discount"}},
		{Bucket: "timing", Patterns: []string{"timing", "timeline", "too early", "too late", "not the right time", "deferred", "deferral", "postponed", "delayed"}},
		{Bucket: "relationship", Patterns: []string{"relationship", "champion", "sponsor left", "sponsor change", "buyer left", "contact left", "stakeholder change", "stakeholder left", "exec change", "no champion", "lost trust"}},
		{Bucket: "no_decision", Patterns: []string{"no decision", "not a priority", "no business case", "stalled", "lost interest", "no action", "indecision"}},
	}
}

// normalizeLossReasonForMatch returns the lower-cased, punctuation-collapsed
// form of raw with one leading and trailing space added so substring patterns
// can be matched as whole tokens.
func normalizeLossReasonForMatch(raw string) string {
	s := strings.ToLower(strings.TrimSpace(raw))
	if s == "" {
		return ""
	}
	replacer := strings.NewReplacer(
		",", " ",
		"/", " ",
		"-", " ",
		".", " ",
		"_", " ",
		"\t", " ",
		"\n", " ",
	)
	return " " + replacer.Replace(s) + " "
}

// NormalizeLossReasonBucket maps a raw CRM loss-reason string to one of the
// deterministic normalized buckets surfaced by the loss_reason dimension.
// Blank input returns an empty string so the dimension's existing <blank>
// coverage row is preserved. Non-empty input that does not match any bucket
// rule returns "unknown_other" so MCP callers can distinguish "no loss
// reason recorded" from "loss reason recorded but unrecognized phrasing".
func NormalizeLossReasonBucket(raw string) string {
	normalized := normalizeLossReasonForMatch(raw)
	if strings.TrimSpace(normalized) == "" {
		return ""
	}
	for _, rule := range lossReasonBucketRules() {
		for _, pattern := range rule.Patterns {
			if strings.Contains(normalized, pattern) {
				return rule.Bucket
			}
		}
	}
	return "unknown_other"
}

// LossReasonBucketWhenClauses returns the WHEN/THEN lines that map a
// normalized (lower-cased, punctuation-collapsed, leading/trailing
// space-padded) loss-reason text aliased as `normAlias` to the
// deterministic loss-reason buckets in priority order. The clauses are
// emitted in the same priority order as NormalizeLossReasonBucket so the
// SQL CASE result and the Go normalization remain in lockstep.
//
// The caller is responsible for the surrounding `CASE` and the empty/raw
// branches; this helper only emits the bucket WHEN clauses so it can be
// reused by both the SQLite scalar-subquery shape and the Postgres
// CREATE FUNCTION shape.
func LossReasonBucketWhenClauses(normAlias string) string {
	var b strings.Builder
	for _, rule := range lossReasonBucketRules() {
		predicates := make([]string, 0, len(rule.Patterns))
		for _, pattern := range rule.Patterns {
			predicates = append(predicates, normAlias+" LIKE '%"+escapeBucketLike(pattern)+"%'")
		}
		b.WriteString("\n\t\tWHEN ")
		b.WriteString(strings.Join(predicates, " OR "))
		b.WriteString(" THEN '")
		b.WriteString(rule.Bucket)
		b.WriteString("'")
	}
	return b.String()
}

// businessAnalysisLossReasonBucketSQL returns the SQLite scalar-subquery
// expression that resolves to one of the normalized loss-reason buckets for
// the call referenced by the cf alias. Raw loss-reason text never escapes
// the subquery; only the bucket label is exposed to the dimension result.
func businessAnalysisLossReasonBucketSQL() string {
	raw := businessAnalysisOpportunityFieldExpr(LossReasonFieldNames)
	return `(
		SELECT CASE
			WHEN TRIM(sub.r) = '' THEN ''` + LossReasonBucketWhenClauses("sub.norm") + `
			ELSE 'unknown_other'
		END
		  FROM (
			SELECT raw_lr.r AS r,
			       ' ' || REPLACE(REPLACE(REPLACE(REPLACE(REPLACE(REPLACE(REPLACE(LOWER(TRIM(raw_lr.r)), ',', ' '), '/', ' '), '-', ' '), '.', ' '), '_', ' '), char(9), ' '), char(10), ' ') || ' ' AS norm
			  FROM (SELECT ` + raw + ` AS r) raw_lr
		  ) sub
	)`
}

// escapeBucketLike escapes characters that would be misinterpreted by
// SQLite/Postgres LIKE matching. The rules use only ASCII letters, digits,
// and spaces today, so this is defensive.
func escapeBucketLike(pattern string) string {
	pattern = strings.ReplaceAll(pattern, "\\", "\\\\")
	pattern = strings.ReplaceAll(pattern, "%", "\\%")
	pattern = strings.ReplaceAll(pattern, "_", "\\_")
	pattern = strings.ReplaceAll(pattern, "'", "''")
	return pattern
}

func businessAnalysisParticipantTitleLabelSQL(callAlias string) string {
	return `COALESCE(NULLIF(TRIM((
		SELECT COALESCE(json_extract(p.value, '$.title'), json_extract(p.value, '$.jobTitle'), json_extract(p.value, '$.job_title'), '')
		  FROM json_each(` + callAlias + `.raw_json, '$.parties') p
		 WHERE TRIM(COALESCE(json_extract(p.value, '$.title'), json_extract(p.value, '$.jobTitle'), json_extract(p.value, '$.job_title'), '')) <> ''
		 LIMIT 1
	)), ''), NULLIF(TRIM((
		SELECT COALESCE(json_extract(p.value, '$.title'), json_extract(p.value, '$.jobTitle'), json_extract(p.value, '$.job_title'), '')
		  FROM json_each(` + callAlias + `.raw_json, '$.metaData.parties') p
		 WHERE TRIM(COALESCE(json_extract(p.value, '$.title'), json_extract(p.value, '$.jobTitle'), json_extract(p.value, '$.job_title'), '')) <> ''
		 LIMIT 1
	)), ''), '')`
}

func businessAnalysisParticipantTitleLikeSQL(callAlias, placeholder string) string {
	return `(
		EXISTS (
			SELECT 1
			  FROM json_each(` + callAlias + `.raw_json, '$.parties') p
			 WHERE LOWER(TRIM(COALESCE(json_extract(p.value, '$.title'), json_extract(p.value, '$.jobTitle'), json_extract(p.value, '$.job_title'), ''))) LIKE '%' || LOWER(` + placeholder + `) || '%'
		)
		OR EXISTS (
			SELECT 1
			  FROM json_each(` + callAlias + `.raw_json, '$.metaData.parties') p
			 WHERE LOWER(TRIM(COALESCE(json_extract(p.value, '$.title'), json_extract(p.value, '$.jobTitle'), json_extract(p.value, '$.job_title'), ''))) LIKE '%' || LOWER(` + placeholder + `) || '%'
		)
		OR EXISTS (
			SELECT 1
			  FROM call_context_objects po
			  JOIN call_context_fields pf
			    ON pf.call_id = po.call_id
			   AND pf.object_key = po.object_key
			 WHERE po.call_id = ` + callAlias + `.call_id
			   AND po.object_type IN ('Contact', 'Lead')
			   AND pf.field_name IN ('Title', 'JobTitle', 'Job_Title__c', 'JobTitle__c')
			   AND LOWER(TRIM(pf.field_value_text)) LIKE '%' || LOWER(` + placeholder + `) || '%'
		)
	)`
}

func coverageHint(populated, total int64, label string) string {
	if total == 0 {
		return "no calls matched the filter"
	}
	if populated == 0 {
		return label + " missing from matched cohort"
	}
	if populated < total {
		return fmt.Sprintf("%s partially populated: %d/%d calls", label, populated, total)
	}
	return label + " populated for matched cohort"
}

func firstPositiveInt(values ...int) int {
	for _, value := range values {
		if value > 0 {
			return value
		}
	}
	return 0
}

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
