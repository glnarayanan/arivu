package qualityops

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"
)

type AuditOptions struct {
	DBPath string
	UserID string
	Now    time.Time
}

type AuditReport struct {
	SchemaVersion string                    `json:"schema_version"`
	GeneratedAt   string                    `json:"generated_at"`
	Scope         string                    `json:"scope"`
	Counts        map[string]int            `json:"counts"`
	Sources       map[string]int            `json:"sources"`
	ContentKinds  map[string]int            `json:"content_kinds"`
	Evidence      EvidenceAudit             `json:"evidence"`
	Summaries     SummaryAudit              `json:"summaries"`
	Semantics     SemanticAudit             `json:"semantics"`
	Feedback      map[string]int            `json:"feedback"`
	VersionDrift  map[string]map[string]int `json:"version_drift"`
	Acceptance    map[string]bool           `json:"acceptance"`
}

type EvidenceAudit struct {
	Statuses             map[string]int `json:"statuses"`
	Reasons              map[string]int `json:"reasons"`
	MissingSelected      int            `json:"missing_selected"`
	MissingPublishedTime int            `json:"missing_published_time"`
	MissingPublisher     int            `json:"missing_publisher"`
}

type SummaryAudit struct {
	Statuses           map[string]int `json:"statuses"`
	ExpansionBuckets   map[string]int `json:"expansion_ratio_buckets"`
	MetadataOnlyClaims int            `json:"metadata_only_claims"`
}

type SemanticAudit struct {
	Entities       int            `json:"entities"`
	Concepts       int            `json:"concepts"`
	KnownJunk      map[string]int `json:"known_junk"`
	SingletonTerms int            `json:"singleton_terms"`
}

func Audit(ctx context.Context, options AuditOptions) (AuditReport, error) {
	if strings.TrimSpace(options.DBPath) == "" {
		return AuditReport{}, fmt.Errorf("database path is required")
	}
	db, err := openReadOnly(options.DBPath)
	if err != nil {
		return AuditReport{}, err
	}
	defer db.Close()
	if err := requireAuditSchema(ctx, db); err != nil {
		return AuditReport{}, err
	}
	now := options.Now.UTC()
	if now.IsZero() {
		now = time.Now().UTC()
	}
	report := AuditReport{
		SchemaVersion: "1",
		GeneratedAt:   now.Format(time.RFC3339),
		Scope:         "all",
		Counts:        map[string]int{},
		Sources:       map[string]int{},
		ContentKinds:  map[string]int{},
		Feedback:      map[string]int{},
		VersionDrift:  map[string]map[string]int{},
		Acceptance:    map[string]bool{},
	}
	if options.UserID != "" {
		report.Scope = "user"
	}
	where, args := auditScope(options.UserID, "b")
	if err := scanCount(ctx, db, `SELECT COUNT(*) FROM bookmarks b WHERE `+where, args, &report.Counts, "bookmarks"); err != nil {
		return AuditReport{}, err
	}
	if err := scanCount(ctx, db, `SELECT COUNT(*) FROM bookmark_evidence e JOIN bookmarks b ON b.id=e.bookmark_id AND b.user_id=e.user_id WHERE `+where, args, &report.Counts, "evidence"); err != nil {
		return AuditReport{}, err
	}
	if err := scanCount(ctx, db, `SELECT COUNT(*) FROM ai_summaries s JOIN bookmarks b ON b.id=s.bookmark_id AND b.user_id=s.user_id WHERE `+where, args, &report.Counts, "summaries"); err != nil {
		return AuditReport{}, err
	}
	if err := groupedCounts(ctx, db, `SELECT COALESCE(NULLIF(b.source,''),'unknown'),COUNT(*) FROM bookmarks b WHERE `+where+` GROUP BY 1 ORDER BY 1`, args, report.Sources); err != nil {
		return AuditReport{}, err
	}
	if err := groupedCounts(ctx, db, `SELECT COALESCE(NULLIF(b.content_kind,''),'unknown'),COUNT(*) FROM bookmarks b WHERE `+where+` GROUP BY 1 ORDER BY 1`, args, report.ContentKinds); err != nil {
		return AuditReport{}, err
	}

	report.Evidence.Statuses = map[string]int{}
	report.Evidence.Reasons = map[string]int{}
	if err := groupedCounts(ctx, db, `SELECT e.quality_status,COUNT(*) FROM bookmark_evidence e JOIN bookmarks b ON b.id=e.bookmark_id AND b.user_id=e.user_id WHERE `+where+` GROUP BY e.quality_status ORDER BY e.quality_status`, args, report.Evidence.Statuses); err != nil {
		return AuditReport{}, err
	}
	if err := groupedCounts(ctx, db, `SELECT j.value,COUNT(*) FROM bookmark_evidence e JOIN bookmarks b ON b.id=e.bookmark_id AND b.user_id=e.user_id JOIN json_each(e.quality_reasons_json) j WHERE `+where+` GROUP BY j.value ORDER BY j.value`, args, report.Evidence.Reasons); err != nil {
		return AuditReport{}, err
	}
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM bookmarks b WHERE `+where+` AND NOT EXISTS (SELECT 1 FROM bookmark_evidence e WHERE e.bookmark_id=b.id AND e.user_id=b.user_id AND e.is_selected=1)`, args...).Scan(&report.Evidence.MissingSelected); err != nil {
		return AuditReport{}, err
	}
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM bookmarks b WHERE `+where+` AND COALESCE(b.source_published_at,'')=''`, args...).Scan(&report.Evidence.MissingPublishedTime); err != nil {
		return AuditReport{}, err
	}
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM bookmarks b WHERE `+where+` AND COALESCE(b.source_publisher_key,'')=''`, args...).Scan(&report.Evidence.MissingPublisher); err != nil {
		return AuditReport{}, err
	}

	report.Summaries.Statuses = map[string]int{}
	report.Summaries.ExpansionBuckets = map[string]int{"none": 0, "under_1x": 0, "1x_to_1_5x": 0, "over_1_5x": 0}
	if err := groupedCounts(ctx, db, `SELECT s.processing_status,COUNT(*) FROM ai_summaries s JOIN bookmarks b ON b.id=s.bookmark_id AND b.user_id=s.user_id WHERE `+where+` GROUP BY s.processing_status ORDER BY s.processing_status`, args, report.Summaries.Statuses); err != nil {
		return AuditReport{}, err
	}
	if err := expansionBuckets(ctx, db, where, args, report.Summaries.ExpansionBuckets); err != nil {
		return AuditReport{}, err
	}
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM ai_summaries s JOIN bookmarks b ON b.id=s.bookmark_id AND b.user_id=s.user_id JOIN bookmark_evidence e ON e.bookmark_id=b.id AND e.user_id=b.user_id AND e.is_selected=1 WHERE `+where+` AND e.quality_status IN ('failed','metadata_only') AND (trim(COALESCE(s.one_sentence,''))<>'' OR trim(COALESCE(s.long_form,''))<>'' OR s.bullet_points_json<>'[]')`, args...).Scan(&report.Summaries.MetadataOnlyClaims); err != nil {
		return AuditReport{}, err
	}

	report.Semantics.KnownJunk = map[string]int{}
	if err := semanticCounts(ctx, db, where, args, &report.Semantics); err != nil {
		return AuditReport{}, err
	}
	feedbackWhere, feedbackArgs := auditScope(options.UserID, "f")
	if err := groupedCounts(ctx, db, `SELECT f.feedback,COUNT(*) FROM knowledge_feedback f WHERE `+feedbackWhere+` GROUP BY f.feedback ORDER BY f.feedback`, feedbackArgs, report.Feedback); err != nil {
		return AuditReport{}, err
	}
	for _, version := range []struct{ name, column string }{{"fetch", "fetch_version"}, {"summary", "summary_version"}, {"enrichment", "enrichment_version"}} {
		values := map[string]int{}
		if err := groupedCounts(ctx, db, `SELECT COALESCE(NULLIF(b.`+version.column+`,''),'missing'),COUNT(*) FROM bookmarks b WHERE `+where+` GROUP BY 1 ORDER BY 1`, args, values); err != nil {
			return AuditReport{}, err
		}
		report.VersionDrift[version.name] = values
	}
	report.Acceptance["no_known_junk_semantics"] = sumMap(report.Semantics.KnownJunk) == 0
	report.Acceptance["no_metadata_only_claims"] = report.Summaries.MetadataOnlyClaims == 0
	report.Acceptance["all_bookmarks_have_selected_evidence"] = report.Evidence.MissingSelected == 0
	return report, nil
}

func requireAuditSchema(ctx context.Context, db *sql.DB) error {
	required := []string{"bookmarks", "bookmark_evidence", "ai_summaries", "bookmark_entities", "bookmark_concepts", "knowledge_feedback"}
	for _, table := range required {
		var count int
		if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?`, table).Scan(&count); err != nil {
			return err
		}
		if count != 1 {
			return fmt.Errorf("database schema is missing %s; deploy the data-quality migration before auditing", table)
		}
	}
	return nil
}

func auditScope(userID, alias string) (string, []any) {
	if userID == "" {
		return "1=1", nil
	}
	return alias + ".user_id=?", []any{userID}
}

func scanCount(ctx context.Context, db *sql.DB, query string, args []any, target *map[string]int, key string) error {
	var count int
	if err := db.QueryRowContext(ctx, query, args...).Scan(&count); err != nil {
		return err
	}
	(*target)[key] = count
	return nil
}

func groupedCounts(ctx context.Context, db queryer, query string, args []any, target map[string]int) error {
	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var key string
		var count int
		if err := rows.Scan(&key, &count); err != nil {
			return err
		}
		target[key] = count
	}
	return rows.Err()
}

func expansionBuckets(ctx context.Context, db *sql.DB, where string, args []any, target map[string]int) error {
	rows, err := db.QueryContext(ctx, `SELECT length(trim(COALESCE(e.content_text,''))),length(trim(COALESCE(s.one_sentence,'')))+length(trim(COALESCE(s.long_form,''))) FROM ai_summaries s JOIN bookmarks b ON b.id=s.bookmark_id AND b.user_id=s.user_id JOIN bookmark_evidence e ON e.bookmark_id=b.id AND e.user_id=b.user_id AND e.is_selected=1 WHERE `+where, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var input, output int
		if err := rows.Scan(&input, &output); err != nil {
			return err
		}
		switch {
		case output == 0 || input == 0:
			target["none"]++
		case output < input:
			target["under_1x"]++
		case float64(output) <= float64(input)*1.5:
			target["1x_to_1_5x"]++
		default:
			target["over_1_5x"]++
		}
	}
	return rows.Err()
}

func semanticCounts(ctx context.Context, db *sql.DB, where string, args []any, target *SemanticAudit) error {
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM bookmark_entities x JOIN bookmarks b ON b.id=x.bookmark_id AND b.user_id=x.user_id WHERE `+where, args...).Scan(&target.Entities); err != nil {
		return err
	}
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM bookmark_concepts x JOIN bookmarks b ON b.id=x.bookmark_id AND b.user_id=x.user_id WHERE `+where, args...).Scan(&target.Concepts); err != nil {
		return err
	}
	junk := []string{"quot", "https", "http", "com", "www", "jun", "june"}
	placeholders := strings.TrimRight(strings.Repeat("?,", len(junk)), ",")
	for _, table := range []struct{ table, column string }{{"bookmark_entities", "entity"}, {"bookmark_concepts", "concept"}} {
		queryArgs := append(append([]any{}, args...), stringArgs(junk)...)
		rows, err := db.QueryContext(ctx, `SELECT lower(x.`+table.column+`),COUNT(*) FROM `+table.table+` x JOIN bookmarks b ON b.id=x.bookmark_id AND b.user_id=x.user_id WHERE `+where+` AND lower(x.`+table.column+`) IN (`+placeholders+`) GROUP BY lower(x.`+table.column+`)`, queryArgs...)
		if err != nil {
			return err
		}
		for rows.Next() {
			var term string
			var count int
			if err := rows.Scan(&term, &count); err != nil {
				rows.Close()
				return err
			}
			target.KnownJunk[term] += count
		}
		rows.Close()
	}
	unionArgs := append(append([]any{}, args...), args...)
	query := `SELECT COUNT(*) FROM (SELECT term FROM (SELECT lower(x.entity) term FROM bookmark_entities x JOIN bookmarks b ON b.id=x.bookmark_id AND b.user_id=x.user_id WHERE ` + where + ` UNION ALL SELECT lower(x.concept) term FROM bookmark_concepts x JOIN bookmarks b ON b.id=x.bookmark_id AND b.user_id=x.user_id WHERE ` + where + `) GROUP BY term HAVING COUNT(*)=1)`
	return db.QueryRowContext(ctx, query, unionArgs...).Scan(&target.SingletonTerms)
}

func stringArgs(values []string) []any {
	result := make([]any, len(values))
	for i := range values {
		result[i] = values[i]
	}
	return result
}

func sumMap(values map[string]int) int {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	total := 0
	for _, key := range keys {
		total += values[key]
	}
	return total
}

func MarshalReport(report AuditReport, format string) ([]byte, error) {
	switch strings.ToLower(strings.TrimSpace(format)) {
	case "", "json":
		return json.MarshalIndent(report, "", "  ")
	case "text":
		return []byte(fmt.Sprintf("Arivu quality audit (%s)\nBookmarks: %d\nEvidence: %d\nSummaries: %d\nKnown junk semantics: %d\nMetadata-only claims: %d\n", report.GeneratedAt, report.Counts["bookmarks"], report.Counts["evidence"], report.Counts["summaries"], sumMap(report.Semantics.KnownJunk), report.Summaries.MetadataOnlyClaims)), nil
	default:
		return nil, fmt.Errorf("unsupported format %q; use json or text", format)
	}
}
