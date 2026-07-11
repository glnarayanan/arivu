package bookmarks

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/glnarayanan/arivu/internal/providers"
)

type qualityEvaluationFixture struct {
	ID          string `json:"id"`
	Scenario    string `json:"scenario"`
	ContentKind string `json:"content_kind"`
	Evidence    struct {
		Kind        string `json:"kind"`
		Text        string `json:"text"`
		Author      string `json:"author"`
		PublishedAt string `json:"published_at"`
	} `json:"evidence"`
	Quality struct {
		Status  string   `json:"status"`
		Reasons []string `json:"reasons"`
	} `json:"quality"`
	AllowedSummaryFacts []string `json:"allowed_summary_facts"`
	ForbiddenClaims     []string `json:"forbidden_claims"`
	ValidEntities       []struct {
		Label string `json:"label"`
		Type  string `json:"type"`
	} `json:"valid_entities"`
	ValidConcepts           []string `json:"valid_concepts"`
	EligibleInsightFamilies []string `json:"eligible_insight_families"`
}

func TestQualityEvaluationCorpusDrivesSummaryAndSemanticValidators(t *testing.T) {
	raw, err := os.ReadFile("testdata/quality/evaluation.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixtures []qualityEvaluationFixture
	if err := json.Unmarshal(raw, &fixtures); err != nil {
		t.Fatal(err)
	}
	var acceptedAllowed, totalAllowed, rejectedForbidden, totalForbidden int
	for _, fixture := range fixtures {
		req := providers.SummaryRequest{
			ContentKind: providers.ContentKind(fixture.ContentKind), PrimaryText: fixture.Evidence.Text,
			QualityStatus: providers.QualityStatus(fixture.Quality.Status), QualityReasons: fixture.Quality.Reasons,
		}
		for _, claim := range fixture.AllowedSummaryFacts {
			totalAllowed++
			if providers.ValidateSummary(req, providers.SummaryResult{OneSentence: claim}) == nil {
				acceptedAllowed++
			}
		}
		for _, claim := range fixture.ForbiddenClaims {
			totalForbidden++
			if providers.ValidateSummary(req, providers.SummaryResult{OneSentence: claim}) != nil {
				rejectedForbidden++
			}
		}
		semanticInput := providers.SemanticResult{}
		for _, entity := range fixture.ValidEntities {
			semanticInput.Entities = append(semanticInput.Entities, providers.SemanticTerm{Label: entity.Label, Type: entity.Type, Confidence: 0.9, Evidence: entity.Label})
		}
		for _, concept := range fixture.ValidConcepts {
			semanticInput.Concepts = append(semanticInput.Concepts, providers.SemanticTerm{Label: concept, Confidence: 0.9, Evidence: concept})
		}
		validated := providers.ValidateSemantics(providers.SemanticRequest{ContentKind: providers.ContentKind(fixture.ContentKind), EvidenceText: fixture.Evidence.Text, QualityStatus: providers.QualityStatus(fixture.Quality.Status)}, semanticInput)
		if fixture.Quality.Status == "complete" && (len(validated.Entities) != len(fixture.ValidEntities) || len(validated.Concepts) != len(fixture.ValidConcepts)) {
			t.Errorf("fixture %q semantics entities=%d/%d concepts=%d/%d", fixture.ID, len(validated.Entities), len(fixture.ValidEntities), len(validated.Concepts), len(fixture.ValidConcepts))
		}
		if (fixture.Quality.Status == "metadata_only" || fixture.Quality.Status == "failed") && (len(validated.Entities) != 0 || len(validated.Concepts) != 0) {
			t.Errorf("fixture %q emitted semantics for %s evidence", fixture.ID, fixture.Quality.Status)
		}
	}
	// These corpus-level ratios keep the deterministic guard measurable without
	// pretending lexical validation can replace the model-output acceptance run.
	if acceptedAllowed*2 < totalAllowed {
		t.Errorf("validator accepted %d/%d supported claims; want at least 50%%", acceptedAllowed, totalAllowed)
	}
	if rejectedForbidden*2 < totalForbidden {
		t.Errorf("validator rejected %d/%d forbidden claims; want at least 50%%", rejectedForbidden, totalForbidden)
	}
}

func TestQualityEvaluationCorpusContract(t *testing.T) {
	raw, err := os.ReadFile("testdata/quality/evaluation.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixtures []qualityEvaluationFixture
	if err := json.Unmarshal(raw, &fixtures); err != nil {
		t.Fatalf("decode quality corpus: %v", err)
	}
	if len(fixtures) < 30 {
		t.Fatalf("quality corpus has %d fixtures, want at least 30", len(fixtures))
	}

	requiredScenarios := map[string]int{
		"short_x":            4,
		"long_x_thread":      3,
		"quoted_x":           3,
		"link_only":          2,
		"media_only":         2,
		"x_article":          3,
		"technical_article":  4,
		"marketing_page":     2,
		"login_wall":         2,
		"table_code_unicode": 3,
		"prompt_injection":   2,
	}
	seenIDs := map[string]bool{}
	seenScenarios := map[string]int{}
	validStatuses := map[string]bool{"complete": true, "partial": true, "metadata_only": true, "failed": true}
	validFamilies := map[string]bool{"emerging_theme": true, "recurring_connection": true, "serendipitous_connection": true, "changed_thinking": true}

	for _, fixture := range fixtures {
		if fixture.ID == "" || fixture.Scenario == "" || fixture.ContentKind == "" || fixture.Evidence.Kind == "" {
			t.Errorf("fixture has missing identity fields: %#v", fixture)
			continue
		}
		if seenIDs[fixture.ID] {
			t.Errorf("duplicate fixture id %q", fixture.ID)
		}
		seenIDs[fixture.ID] = true
		seenScenarios[fixture.Scenario]++
		if !validStatuses[fixture.Quality.Status] {
			t.Errorf("fixture %q has unsupported quality status %q", fixture.ID, fixture.Quality.Status)
		}
		if len(fixture.ForbiddenClaims) == 0 {
			t.Errorf("fixture %q must include at least one forbidden claim", fixture.ID)
		}
		if (fixture.Quality.Status == "failed" || fixture.Quality.Status == "metadata_only") && len(fixture.AllowedSummaryFacts) != 0 {
			t.Errorf("fixture %q permits summary claims for %s evidence", fixture.ID, fixture.Quality.Status)
		}
		if fixture.Quality.Status != "complete" && len(fixture.Quality.Reasons) == 0 {
			t.Errorf("fixture %q must explain non-complete evidence", fixture.ID)
		}
		for _, allowed := range fixture.AllowedSummaryFacts {
			for _, forbidden := range fixture.ForbiddenClaims {
				if strings.EqualFold(strings.TrimSpace(allowed), strings.TrimSpace(forbidden)) {
					t.Errorf("fixture %q has the same allowed and forbidden claim %q", fixture.ID, allowed)
				}
			}
		}
		for _, entity := range fixture.ValidEntities {
			if entity.Label == "" || entity.Type == "" {
				t.Errorf("fixture %q has an incomplete entity label", fixture.ID)
			}
			if !strings.Contains(strings.ToLower(fixture.Evidence.Text), strings.ToLower(entity.Label)) {
				t.Errorf("fixture %q entity %q has no evidence span", fixture.ID, entity.Label)
			}
		}
		for _, concept := range fixture.ValidConcepts {
			if concept == "" || !strings.Contains(strings.ToLower(fixture.Evidence.Text), strings.ToLower(concept)) {
				t.Errorf("fixture %q concept %q has no evidence span", fixture.ID, concept)
			}
		}
		for _, family := range fixture.EligibleInsightFamilies {
			if !validFamilies[family] {
				t.Errorf("fixture %q has unsupported insight family %q", fixture.ID, family)
			}
		}
	}

	for scenario, minimum := range requiredScenarios {
		if seenScenarios[scenario] < minimum {
			t.Errorf("scenario %q has %d fixtures, want at least %d", scenario, seenScenarios[scenario], minimum)
		}
	}

	corpus := strings.ToLower(string(raw))
	for _, forbidden := range []string{"twitter.com/", "x.com/", "t.co/", "@tbl", "/var/lib/arivu"} {
		if strings.Contains(corpus, forbidden) {
			t.Errorf("quality corpus contains production-like identifier %q", forbidden)
		}
	}
}
