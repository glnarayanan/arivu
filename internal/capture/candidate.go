// Package capture defines capture-engine-neutral contracts used to choose the
// reader projection from independently collected evidence.
package capture

// Source identifies how a candidate was obtained. It is deliberately smaller
// than extractor-specific method names so selection policy stays stable when an
// implementation changes.
type Source string

const (
	SourceNative     Source = "source_native"
	SourceCurrentTab Source = "current_tab"
	SourceRendered   Source = "rendered"
	SourceDirect     Source = "direct"
)

// Quality is the capture completeness classification shared by direct and
// browser-rendered extraction.
type Quality string

const (
	QualityComplete     Quality = "complete"
	QualityPartial      Quality = "partial"
	QualityMetadataOnly Quality = "metadata_only"
	QualityFailed       Quality = "failed"
)

// Candidate contains only the fields needed to choose reader evidence. The
// selected evidence row remains the source of truth for the full content.
type Candidate struct {
	ID        string
	Source    Source
	Quality   Quality
	Score     int
	Challenge bool
	Empty     bool
}

// SelectReaderEvidence applies Arivu's stable evidence priority. current is
// included so a failed re-capture cannot replace the last known good reader.
func SelectReaderEvidence(current Candidate, candidates []Candidate) (Candidate, bool) {
	best := Candidate{}
	bestRank := -1
	consider := func(candidate Candidate) {
		rank := candidateRank(candidate)
		if rank < 0 {
			return
		}
		// A newly captured candidate replaces equally good current evidence. This
		// lets an explicit Reprocess refresh extractor output while lower-quality,
		// empty, failed, and challenged candidates remain ineligible above.
		if rank > bestRank || (rank == bestRank && candidate.Score >= best.Score) {
			best, bestRank = candidate, rank
		}
	}
	consider(current)
	for _, candidate := range candidates {
		consider(candidate)
	}
	return best, bestRank >= 0
}

func candidateRank(candidate Candidate) int {
	if candidate.ID == "" || candidate.Empty || candidate.Challenge || candidate.Quality == QualityFailed {
		return -1
	}
	if candidate.Quality == QualityMetadataOnly {
		return 1
	}
	switch candidate.Source {
	case SourceNative:
		if candidate.Quality == QualityComplete {
			return 8
		}
		if candidate.Quality == QualityPartial {
			return 4
		}
	case SourceCurrentTab:
		if candidate.Quality == QualityComplete {
			return 7
		}
		if candidate.Quality == QualityPartial {
			return 4
		}
	case SourceRendered:
		if candidate.Quality == QualityComplete {
			return 6
		}
		if candidate.Quality == QualityPartial {
			return 4
		}
	case SourceDirect:
		if candidate.Quality == QualityComplete {
			return 5
		}
		if candidate.Quality == QualityPartial {
			return 3
		}
	}
	return -1
}
