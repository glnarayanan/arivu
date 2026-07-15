package providers

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestSummaryPolicyRejectsClaimsForMetadataOnlyEvidence(t *testing.T) {
	result, err := (GeminiClient{}).GenerateSummary(context.Background(), SummaryRequest{
		ContentKind:   ContentKindWebPage,
		PrimaryText:   "Sign in to continue",
		QualityStatus: QualityMetadataOnly,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != SummaryStatusInsufficientEvidence || result.OneSentence != "" || len(result.BulletPoints) != 0 {
		t.Fatalf("metadata-only result = %#v", result)
	}
}

func TestValidateSummaryAdaptsToShortSocialEvidence(t *testing.T) {
	req := SummaryRequest{
		ContentKind:   ContentKindXPost,
		PrimaryText:   "SQLite WAL mode lets readers continue while one writer commits.",
		QualityStatus: QualityComplete,
	}
	result := SummaryResult{
		OneSentence:  "SQLite WAL mode lets readers continue while one writer commits.",
		LongForm:     strings.Repeat("Unsupported expansion. ", 20),
		BulletPoints: []string{"SQLite WAL mode lets readers continue.", "One writer commits.", "A third forced point."},
	}
	err := ValidateSummary(req, result)
	var validationErr *SummaryValidationError
	if !errors.As(err, &validationErr) {
		t.Fatalf("error = %v, want SummaryValidationError", err)
	}
	for _, want := range []string{"long_form_not_allowed", "too_many_bullet_points"} {
		if !containsString(validationErr.ReasonCodes, want) {
			t.Errorf("reason codes %v do not contain %q", validationErr.ReasonCodes, want)
		}
	}
}

func TestInvalidOptionalTagsDoNotDiscardAnOtherwiseValidSummary(t *testing.T) {
	req := SummaryRequest{
		ContentKind: ContentKindArticle, PrimaryText: "The guide covers Claude Code workflows and context engineering.", QualityStatus: QualityComplete,
	}
	result := SummaryResult{
		OneSentence: "The guide covers Claude Code workflows and context engineering.", SuggestedTags: []string{"not a valid tag!"},
	}
	repaired, err := dropInvalidOptionalTags(req, result, ValidateSummary(req, result))
	if err != nil || repaired.OneSentence != result.OneSentence || len(repaired.SuggestedTags) != 0 {
		t.Fatalf("repaired result = %#v, err = %v", repaired, err)
	}
}

func TestSalvageSummaryKeepsIndependentlyValidRichFields(t *testing.T) {
	req := SummaryRequest{
		ContentKind:   ContentKindArticle,
		PrimaryText:   strings.Repeat("Claude Code supports sub-agents for focused tasks. Hooks run commands at defined lifecycle points. Skills package reusable instructions for coding agents. ", 8),
		QualityStatus: QualityComplete,
	}
	candidate := SummaryResult{
		OneSentence:  "Oracle provides an unsupported summary that is deliberately much too long for the concise one sentence field and must be replaced safely.",
		LongForm:     "Claude Code supports sub-agents for focused tasks. Hooks run commands at defined lifecycle points.",
		BulletPoints: []string{"Skills package reusable instructions for coding agents."},
		Highlights:   []string{"Hooks run commands at defined lifecycle points."},
	}
	got := SalvageSummary(req, candidate, "Claude Code supports sub-agents for focused tasks.")
	if got.OneSentence != "Claude Code supports sub-agents for focused tasks." || got.LongForm == "" || len(got.BulletPoints) != 1 || len(got.Highlights) != 1 {
		t.Fatalf("salvaged summary = %#v", got)
	}
	if err := ValidateSummary(req, got); err != nil {
		t.Fatalf("salvaged summary is invalid: %v", err)
	}
}

func TestValidateSummaryRejectsUnsupportedFactsRecommendationsAndDuplicates(t *testing.T) {
	req := SummaryRequest{
		ContentKind:   ContentKindXPost,
		PrimaryText:   "Tomorrow I will compare Model Cedar with Model Birch on a small retrieval task.",
		QualityStatus: QualityComplete,
	}
	result := SummaryResult{
		OneSentence:  "Model Cedar won by 40 percent.",
		BulletPoints: []string{"Model Cedar won.", "Model Cedar won!"},
	}
	err := ValidateSummary(req, result)
	var validationErr *SummaryValidationError
	if !errors.As(err, &validationErr) {
		t.Fatalf("error = %v, want SummaryValidationError", err)
	}
	for _, want := range []string{"unsupported_number", "unsupported_recommendation_or_result", "duplicate_output"} {
		if !containsString(validationErr.ReasonCodes, want) {
			t.Errorf("reason codes %v do not contain %q", validationErr.ReasonCodes, want)
		}
	}
}

func TestValidateSummaryRejectsUnsupportedNamedTechnology(t *testing.T) {
	err := ValidateSummary(SummaryRequest{
		ContentKind: ContentKindArticle, PrimaryText: "SQLite keeps the data local.", QualityStatus: QualityComplete,
	}, SummaryResult{OneSentence: "The PostgreSQL database keeps the data local."})
	var validationErr *SummaryValidationError
	if !errors.As(err, &validationErr) || !containsString(validationErr.ReasonCodes, "unsupported_named_entity") {
		t.Fatalf("error = %v", err)
	}
}

func TestValidateSummaryRejectsUnsupportedOrdinaryProperNounAtSentenceStart(t *testing.T) {
	err := ValidateSummary(SummaryRequest{
		ContentKind: ContentKindArticle, PrimaryText: "SQLite keeps the data local.", QualityStatus: QualityComplete,
	}, SummaryResult{OneSentence: "Oracle keeps the data local."})
	var validationErr *SummaryValidationError
	if !errors.As(err, &validationErr) || !containsString(validationErr.ReasonCodes, "unsupported_named_entity") {
		t.Fatalf("error = %v", err)
	}
}

func TestValidateSemanticsRequiresEvidenceAndDropsJunk(t *testing.T) {
	req := SemanticRequest{ContentKind: ContentKindArticle, EvidenceText: "Microsoft documents row-level security as a database policy mechanism.", QualityStatus: QualityComplete}
	result := SemanticResult{
		Entities: []SemanticTerm{
			{Label: "Microsoft", Type: "organization", Confidence: 0.98, Evidence: "Microsoft"},
			{Label: "https", Type: "technology", Confidence: 0.99, Evidence: "https"},
			{Label: "PostgreSQL", Type: "technology", Confidence: 0.99, Evidence: "PostgreSQL"},
			{Label: "&quot;", Type: "artifact", Confidence: 1, Evidence: "Microsoft"},
		},
		Concepts: []SemanticTerm{
			{Label: "row-level security", Confidence: 0.91, Evidence: "row-level security"},
			{Label: "database policy", Confidence: 0.30, Evidence: "database policy"},
		},
	}
	got := ValidateSemantics(req, result)
	if len(got.Entities) != 1 || got.Entities[0].Label != "Microsoft" || got.Entities[0].NormalizedKey != "microsoft" {
		t.Fatalf("entities = %#v", got.Entities)
	}
	if len(got.Concepts) != 1 || got.Concepts[0].Label != "row-level security" || got.Concepts[0].NormalizedKey != "row-level security" {
		t.Fatalf("concepts = %#v", got.Concepts)
	}
	if got.Concepts[0].EvidenceStart < 0 || got.Concepts[0].EvidenceEnd <= got.Concepts[0].EvidenceStart {
		t.Fatalf("concept evidence locator = %#v", got.Concepts[0])
	}
}

func TestValidateSemanticsNormalizesAliasesAndAllowsZero(t *testing.T) {
	req := SemanticRequest{ContentKind: ContentKindArticle, EvidenceText: "SQLite and sqlite support local storage.", QualityStatus: QualityComplete}
	got := ValidateSemantics(req, SemanticResult{Entities: []SemanticTerm{
		{Label: "SQLite", Type: "technology", Confidence: 0.9, Evidence: "SQLite"},
		{Label: "sqlite", Type: "technology", Confidence: 0.85, Evidence: "sqlite"},
	}})
	if len(got.Entities) != 1 || got.Entities[0].Label != "SQLite" {
		t.Fatalf("normalized entities = %#v", got.Entities)
	}

	empty := ValidateSemantics(SemanticRequest{QualityStatus: QualityFailed, EvidenceText: "quot https com"}, SemanticResult{
		Concepts: []SemanticTerm{{Label: "quot", Confidence: 1, Evidence: "quot"}},
	})
	if len(empty.Entities) != 0 || len(empty.Concepts) != 0 {
		t.Fatalf("failed evidence semantics = %#v", empty)
	}
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
