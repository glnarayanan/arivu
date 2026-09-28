package bookmarks

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/glnarayanan/arivu/internal/providers"
)

type qualityEvaluationFixture struct {
	ID          string `json:"id"`
	ContentKind string `json:"content_kind"`
	Evidence    struct {
		Text string `json:"text"`
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
	ValidConcepts []string `json:"valid_concepts"`
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
