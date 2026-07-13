package bookmarks

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"slices"
	"strings"

	"github.com/glnarayanan/arivu/internal/browsercapture"
	"github.com/glnarayanan/arivu/internal/capture"
	"github.com/glnarayanan/arivu/internal/ids"
	"github.com/glnarayanan/arivu/internal/safefetch"
)

type directCapture struct {
	result safefetch.Result
	err    error
}

type renderedCapture struct {
	evidence     BookmarkEvidence
	metadata     captureMetadata
	mediaBatchID string
	err          error
}

type captureMetadata struct {
	title       string
	description string
	domain      string
}

func (s *Service) processWebBookmarkAttempt(ctx context.Context, userID, bookmarkID, rawURL, attemptID, currentTitle, currentDescription, currentDomain string) error {
	renderedRequired := s.browser.Enabled && s.browser.Protocol == 2 && attemptID != ""
	directChannel := make(chan directCapture, 1)
	go func() {
		if s.fetchPage == nil {
			directChannel <- directCapture{err: errors.New("fetcher unavailable")}
			return
		}
		result, err := s.fetchPage(ctx, rawURL)
		directChannel <- directCapture{result: result, err: err}
	}()

	renderedChannel := make(chan renderedCapture, 1)
	if renderedRequired {
		go func() { renderedChannel <- s.captureRendered(ctx, userID, bookmarkID, rawURL, attemptID) }()
	} else {
		renderedChannel <- renderedCapture{}
	}

	direct := <-directChannel
	storedDirect, directStoreErr := s.storeDirectCapture(ctx, userID, bookmarkID, attemptID, rawURL, direct)
	directMetadata := captureMetadata{title: fallback(direct.result.Title, direct.result.Domain), description: direct.result.Description, domain: direct.result.Domain}
	if directStoreErr != nil && attemptID != "" {
		code := "direct_capture_store_failed"
		if storedDirect.ID != "" {
			code = "source_artifact_failed"
		}
		_, _ = s.db.ExecContext(ctx, `UPDATE capture_attempts SET error_code=? WHERE id=? AND user_id=?`, code, attemptID, userID)
	}
	if storedDirect.ID != "" {
		evidenceRows, err := s.Evidence(ctx, userID, bookmarkID)
		if err != nil {
			return err
		}
		current := capture.Candidate{}
		for _, evidence := range evidenceRows {
			if evidence.Selected {
				current = evidenceCandidate(evidence, evidenceCaptureSource(evidence), false)
				break
			}
		}
		preliminary, ok := capture.SelectReaderEvidence(current, []capture.Candidate{evidenceCandidate(storedDirect, capture.SourceDirect, false)})
		if ok && preliminary.ID == storedDirect.ID && preliminary.ID != current.ID {
			if _, err := s.activateSelectedEvidence(ctx, userID, bookmarkID, directMetadata.title, directMetadata.description, directMetadata.domain, storedDirect, attemptID); err != nil {
				return err
			}
		}
	}

	rendered := <-renderedChannel
	if rendered.mediaBatchID != "" {
		defer s.discardCaptureMedia(ctx, userID, bookmarkID, rendered.mediaBatchID)
	}

	evidenceRows, err := s.Evidence(ctx, userID, bookmarkID)
	if err != nil {
		return err
	}
	current := capture.Candidate{}
	currentEvidence := BookmarkEvidence{}
	for _, evidence := range evidenceRows {
		if evidence.Selected {
			current = evidenceCandidate(evidence, evidenceCaptureSource(evidence), false)
			currentEvidence = evidence
		}
	}
	candidates := make([]capture.Candidate, 0, 2)
	if storedDirect.ID != "" {
		candidates = append(candidates, evidenceCandidate(storedDirect, capture.SourceDirect, false))
	}
	if rendered.evidence.ID != "" {
		candidates = append(candidates, evidenceCandidate(rendered.evidence, capture.SourceRendered, slices.Contains(rendered.evidence.QualityReasons, "challenge_detected")))
	}
	selected, ok := capture.SelectReaderEvidence(current, candidates)
	if !ok {
		if directStoreErr != nil {
			return directStoreErr
		}
		if direct.err != nil {
			return direct.err
		}
		if rendered.err != nil {
			return rendered.err
		}
		return errPartialExtraction
	}
	selectedEvidence := currentEvidence
	selectedMetadata := captureMetadata{title: currentTitle, description: currentDescription, domain: currentDomain}
	if storedDirect.ID != "" && selected.ID == storedDirect.ID {
		selectedEvidence = storedDirect
		selectedMetadata = directMetadata
	}
	if rendered.evidence.ID != "" && selected.ID == rendered.evidence.ID {
		selectedEvidence = rendered.evidence
		selectedMetadata = rendered.metadata
	}
	mediaBatchID := ""
	if rendered.evidence.ID != "" && selectedEvidence.ID == rendered.evidence.ID {
		mediaBatchID = rendered.mediaBatchID
	}
	if err := s.persistSelectedEvidenceForAttemptWithMedia(ctx, userID, bookmarkID, selectedMetadata.title, selectedMetadata.description, selectedMetadata.domain, selectedEvidence, attemptID, mediaBatchID); err != nil {
		return err
	}
	directComplete := direct.err == nil && storedDirect.QualityStatus == string(capture.QualityComplete)
	if s.browser.Enabled && s.browser.Protocol == 1 && attemptID != "" && s.assets != nil && directComplete {
		if err := s.captureLegacyArtifacts(ctx, userID, bookmarkID, attemptID, selectedEvidence, selectedEvidence.CanonicalURL); err != nil {
			_, _ = s.db.ExecContext(ctx, `UPDATE capture_attempts SET error_code=? WHERE id=? AND user_id=?`, browserCaptureErrorCode(err), attemptID, userID)
			return errPartialExtraction
		}
	}
	renderedComplete := rendered.err == nil && rendered.evidence.QualityStatus == string(capture.QualityComplete)
	if selectedEvidence.QualityStatus != string(capture.QualityComplete) || directStoreErr != nil || (!directComplete && !renderedComplete) || (renderedRequired && !renderedComplete) {
		return errPartialExtraction
	}
	return nil
}

func (s *Service) captureRendered(ctx context.Context, userID, bookmarkID, rawURL, attemptID string) renderedCapture {
	captured := renderedCapture{mediaBatchID: ids.New()}
	err := browsercapture.RunV2(ctx, s.browser, rawURL, func(result browsercapture.V2Result) error {
		htmlBody, err := os.ReadFile(result.Content.HTML.Path)
		if err != nil {
			return err
		}
		textBody, err := os.ReadFile(result.Content.Text.Path)
		if err != nil {
			return err
		}
		canonical := fallback(result.Metadata.CanonicalURL, result.Metadata.FinalURL)
		storedMedia, mediaErr := s.storeCaptureMedia(ctx, userID, bookmarkID, attemptID, captured.mediaBatchID, result.Media)
		mediaLinked := false
		defer func() {
			if !mediaLinked {
				s.discardCaptureMedia(ctx, userID, bookmarkID, captured.mediaBatchID)
			}
		}()
		htmlBody = []byte(rewriteReaderMedia(string(htmlBody), canonical, storedMedia))
		evidence, err := s.storeRenderedEvidenceWithMedia(ctx, userID, bookmarkID, attemptID, captured.mediaBatchID, BookmarkEvidence{
			Kind: "rendered_article", Origin: "browser_render", Authority: 75,
			Text: string(textBody), SanitizedHTML: string(htmlBody), CanonicalURL: canonical,
			PublisherKey: publisherForURL(canonical), PublishedAt: result.Metadata.PublishedAt,
			ExtractionMethod: "mozilla_readability", QualityStatus: string(result.Content.QualityStatus),
			QualityScore: result.Content.QualityScore, QualityReasons: result.Content.QualityReasons,
			ExtractorVersion: result.EngineVersion + "+attempt-" + attemptID + "+batch-" + captured.mediaBatchID,
		}, storedMedia)
		if err != nil {
			return err
		}
		mediaLinked = true
		captured.evidence = evidence
		captured.metadata = captureMetadata{title: result.Metadata.Title, description: result.Metadata.Description, domain: publisherForURL(canonical)}
		if s.assets != nil {
			for _, artifact := range result.Artifacts {
				file, err := os.Open(artifact.Path)
				if err != nil {
					return err
				}
				key, digest, size, storeErr := s.assets.Put(io.LimitReader(file, s.browser.MaxFileBytes+1))
				file.Close()
				if storeErr != nil {
					return storeErr
				}
				if err := s.commitStagedArtifact(ctx, ids.New(), userID, bookmarkID, attemptID, captured.mediaBatchID, evidence.ID, artifact.Type, artifact.MIME, size, digest, key); err != nil {
					return err
				}
			}
		}
		_, _ = s.db.ExecContext(ctx, `UPDATE capture_attempts SET final_url=?,engine='direct_http+browser',engine_version=? WHERE id=? AND user_id=?`, result.Metadata.FinalURL, result.EngineVersion, attemptID, userID)
		return mediaErr
	})
	captured.err = err
	if err != nil && attemptID != "" {
		_, _ = s.db.ExecContext(ctx, `UPDATE capture_attempts SET error_code=? WHERE id=? AND user_id=?`, browserCaptureErrorCode(err), attemptID, userID)
	}
	return captured
}

func (s *Service) storeDirectCapture(ctx context.Context, userID, bookmarkID, attemptID, rawURL string, direct directCapture) (BookmarkEvidence, error) {
	if direct.err != nil {
		_, _ = s.UpsertEvidence(ctx, userID, bookmarkID, BookmarkEvidence{
			Kind: "fetched_article", Origin: "web_fetch", CanonicalURL: rawURL, ExtractionMethod: "generic_web",
			QualityStatus: "failed", QualityReasons: []string{safefetch.FailureReason(direct.err)}, ExtractorVersion: safefetch.ExtractorVersion,
		})
		return BookmarkEvidence{}, nil
	}
	result := direct.result
	evidence, err := s.UpsertEvidence(ctx, userID, bookmarkID, BookmarkEvidence{
		Kind: "fetched_article", Origin: "web_fetch", Authority: 70, Text: result.Text, SanitizedHTML: result.HTML,
		CanonicalURL: result.URL, PublisherKey: result.Domain, ExtractionMethod: result.Quality.Method,
		QualityStatus: string(result.Quality.Status), QualityScore: result.Quality.Score,
		QualityReasons: result.Quality.Reasons, ExtractorVersion: result.Quality.Version,
	})
	if err != nil {
		return BookmarkEvidence{}, err
	}
	if s.assets != nil && attemptID != "" && len(result.Body) > 0 {
		key, digest, size, err := s.assets.Put(bytes.NewReader(result.Body))
		if err != nil {
			return evidence, err
		}
		mime := strings.TrimSpace(strings.Split(result.ContentType, ";")[0])
		if mime == "" {
			mime = "application/octet-stream"
		}
		if err := s.commitArtifact(ctx, ids.New(), userID, bookmarkID, attemptID, evidence.ID, "source_response", mime, size, digest, key); err != nil {
			return evidence, err
		}
		_, _ = s.db.ExecContext(ctx, `UPDATE capture_attempts SET final_url=?,engine_version=? WHERE id=? AND user_id=?`, result.URL, safefetch.ExtractorVersion, attemptID, userID)
	}
	return evidence, nil
}

func browserCaptureErrorCode(err error) string {
	var captureError *browsercapture.Error
	if errors.As(err, &captureError) {
		return captureError.Code
	}
	return "browser_capture_failed"
}

func evidenceCandidate(evidence BookmarkEvidence, source capture.Source, challenge bool) capture.Candidate {
	return capture.Candidate{
		ID: evidence.ID, Source: source, Quality: capture.Quality(evidence.QualityStatus), Score: evidence.QualityScore,
		Challenge: challenge, Empty: strings.TrimSpace(evidence.Text) == "" && strings.TrimSpace(evidence.SanitizedHTML) == "",
	}
}

func evidenceCaptureSource(evidence BookmarkEvidence) capture.Source {
	switch evidence.Origin {
	case "x_api", "source_native":
		return capture.SourceNative
	case "current_tab", "extension_capture", "singlefile":
		return capture.SourceCurrentTab
	case "browser_render":
		return capture.SourceRendered
	default:
		return capture.SourceDirect
	}
}

func (s *Service) captureLegacyArtifacts(ctx context.Context, userID, bookmarkID, attemptID string, evidence BookmarkEvidence, rawURL string) error {
	return browsercapture.Run(ctx, s.browser, rawURL, func(artifact browsercapture.Artifact, reader io.Reader) error {
		key, digest, size, err := s.assets.Put(reader)
		if err != nil {
			return err
		}
		return s.commitArtifact(ctx, ids.New(), userID, bookmarkID, attemptID, evidence.ID, artifact.Type, artifact.MIME, size, digest, key)
	})
}
