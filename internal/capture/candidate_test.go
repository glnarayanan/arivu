package capture

import "testing"

func TestSelectReaderEvidencePriority(t *testing.T) {
	tests := []struct {
		name       string
		current    Candidate
		candidates []Candidate
		wantID     string
	}{
		{
			name:    "source native outranks every web extraction",
			current: Candidate{ID: "old", Source: SourceDirect, Quality: QualityComplete, Score: 90},
			candidates: []Candidate{
				{ID: "rendered", Source: SourceRendered, Quality: QualityComplete, Score: 100},
				{ID: "native", Source: SourceNative, Quality: QualityComplete, Score: 80},
			},
			wantID: "native",
		},
		{
			name:    "explicit current tab outranks rendered and direct captures",
			current: Candidate{ID: "old", Source: SourceDirect, Quality: QualityComplete, Score: 90},
			candidates: []Candidate{
				{ID: "direct", Source: SourceDirect, Quality: QualityComplete, Score: 100},
				{ID: "rendered", Source: SourceRendered, Quality: QualityComplete, Score: 90},
				{ID: "tab", Source: SourceCurrentTab, Quality: QualityComplete, Score: 70},
			},
			wantID: "tab",
		},
		{
			name:    "complete rendered outranks complete direct",
			current: Candidate{},
			candidates: []Candidate{
				{ID: "direct", Source: SourceDirect, Quality: QualityComplete, Score: 100},
				{ID: "rendered", Source: SourceRendered, Quality: QualityComplete, Score: 60},
			},
			wantID: "rendered",
		},
		{
			name:    "complete direct outranks partial rendered",
			current: Candidate{},
			candidates: []Candidate{
				{ID: "rendered", Source: SourceRendered, Quality: QualityPartial, Score: 100},
				{ID: "direct", Source: SourceDirect, Quality: QualityComplete, Score: 60},
			},
			wantID: "direct",
		},
		{
			name:    "partial capture cannot replace last known complete evidence",
			current: Candidate{ID: "old", Source: SourceDirect, Quality: QualityComplete, Score: 55},
			candidates: []Candidate{
				{ID: "challenge", Source: SourceRendered, Quality: QualityPartial, Score: 100, Challenge: true},
				{ID: "metadata", Source: SourceDirect, Quality: QualityMetadataOnly, Score: 100},
			},
			wantID: "old",
		},
		{
			name:    "partial source native cannot replace last known complete evidence",
			current: Candidate{ID: "old", Source: SourceDirect, Quality: QualityComplete, Score: 55},
			candidates: []Candidate{
				{ID: "native", Source: SourceNative, Quality: QualityPartial, Score: 100},
				{ID: "tab", Source: SourceCurrentTab, Quality: QualityPartial, Score: 100},
			},
			wantID: "old",
		},
		{
			name:    "unknown source quality cannot replace last known complete evidence",
			current: Candidate{ID: "old", Source: SourceRendered, Quality: QualityComplete, Score: 55},
			candidates: []Candidate{
				{ID: "native", Source: SourceNative, Score: 100},
				{ID: "tab", Source: SourceCurrentTab, Score: 100},
			},
			wantID: "old",
		},
		{
			name:    "empty complete claim is ineligible",
			current: Candidate{},
			candidates: []Candidate{
				{ID: "empty", Source: SourceRendered, Quality: QualityComplete, Score: 100, Empty: true},
				{ID: "partial", Source: SourceDirect, Quality: QualityPartial, Score: 50},
			},
			wantID: "partial",
		},
		{
			name:    "score breaks ties within the same source and quality",
			current: Candidate{},
			candidates: []Candidate{
				{ID: "lower", Source: SourceRendered, Quality: QualityComplete, Score: 70},
				{ID: "higher", Source: SourceRendered, Quality: QualityComplete, Score: 90},
			},
			wantID: "higher",
		},
		{
			name:    "equally good recapture refreshes current evidence",
			current: Candidate{ID: "old", Source: SourceRendered, Quality: QualityComplete, Score: 95},
			candidates: []Candidate{
				{ID: "refreshed", Source: SourceRendered, Quality: QualityComplete, Score: 95},
			},
			wantID: "refreshed",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := SelectReaderEvidence(tt.current, tt.candidates)
			if !ok || got.ID != tt.wantID {
				t.Fatalf("SelectReaderEvidence() = %#v, %v; want id %q", got, ok, tt.wantID)
			}
		})
	}
}

func TestSelectReaderEvidenceRejectsOnlyUnsafeCandidates(t *testing.T) {
	got, ok := SelectReaderEvidence(Candidate{}, []Candidate{
		{ID: "challenge", Source: SourceRendered, Quality: QualityComplete, Challenge: true},
		{ID: "empty", Source: SourceDirect, Quality: QualityComplete, Empty: true},
	})
	if ok || got.ID != "" {
		t.Fatalf("SelectReaderEvidence() = %#v, %v; want no selection", got, ok)
	}
}
