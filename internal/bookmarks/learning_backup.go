package bookmarks

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

func decodeLearningBackup(raw any) ([]learningSession, error) {
	if raw == nil {
		return nil, nil
	}
	data, err := json.Marshal(raw)
	if err != nil {
		return nil, err
	}
	var sessions []learningSession
	if json.Unmarshal(data, &sessions) != nil {
		return nil, errors.New("invalid learning sessions")
	}
	for _, session := range sessions {
		if session.ID == "" || (session.Kind != "chat" && session.Kind != "quiz") || len(session.Passages) > 16 || len(session.Passages) == 0 || len(session.Exchanges) > 6 || (len(session.Questions) != 0 && len(session.Questions) != 3) || (len(session.Choices) != 0 && len(session.Choices) != len(session.Questions)) {
			return nil, errors.New("invalid learning session")
		}
		evidence := map[string]string{}
		for _, passage := range session.Passages {
			if passage.ID == "" || evidence[passage.ID] != "" || len(passage.Text) == 0 || len(passage.Text) > 1500 || (passage.Source.Type != "bookmark" && passage.Source.Type != "note") {
				return nil, errors.New("invalid learning passage")
			}
			evidence[passage.ID] = passage.Text
		}
		for _, exchange := range session.Exchanges {
			if len(exchange.Question) > 2000 || len(exchange.Answer.Claims) > 8 {
				return nil, errors.New("invalid learning answer")
			}
			for _, claim := range exchange.Answer.Claims {
				if len(claim.Text) > 4000 || len(claim.Citations) == 0 || len(claim.Citations) > 4 {
					return nil, errors.New("invalid learning claim")
				}
				for _, citation := range claim.Citations {
					if citation.Quote == "" || !strings.Contains(evidence[citation.PassageID], citation.Quote) {
						return nil, errors.New("invalid learning citation")
					}
				}
			}
		}
		for _, question := range session.Questions {
			if len(question.Options) != 4 || question.Correct < 0 || question.Correct > 3 || len(question.Question) > 2000 || len(question.Explanation) > 4000 {
				return nil, errors.New("invalid quiz question")
			}
			for _, citation := range question.Citations {
				if citation.Quote == "" || !strings.Contains(evidence[citation.PassageID], citation.Quote) {
					return nil, errors.New("invalid quiz citation")
				}
			}
		}
		for _, choice := range session.Choices {
			if choice < 0 || choice > 3 {
				return nil, errors.New("invalid quiz choice")
			}
		}
	}
	return sessions, nil
}

func (s *Service) exportLearning(ctx context.Context, userID string) ([]learningSession, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT payload_json FROM learning_sessions WHERE user_id=? ORDER BY created_at,id`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	sessions := []learningSession{}
	for rows.Next() {
		var raw string
		if err = rows.Scan(&raw); err != nil {
			return nil, err
		}
		var session learningSession
		if err = json.Unmarshal([]byte(raw), &session); err != nil {
			return nil, err
		}
		sessions = append(sessions, session)
	}
	return sessions, rows.Err()
}

func (s *Service) restoreLearning(ctx context.Context, userID string, sessions []learningSession, bookmarks, notes map[string]string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, session := range sessions {
		// Replay identical backups without discarding a later, different snapshot
		// or overwriting a conversation continued locally after import.
		original, err := json.Marshal(session)
		if err != nil {
			return err
		}
		session.ID = stableKnowledgeID("learning", userID, session.ID, fmt.Sprintf("%x", sha256.Sum256(original)))
		session.Revision = 0
		for i := range session.Passages {
			source := &session.Passages[i].Source
			source.ID = remapItemID(source.Type, source.ID, bookmarks, notes)
		}
		raw, err := json.Marshal(session)
		if err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO learning_sessions(id,user_id,payload_json,created_at,updated_at) VALUES(?,?,?,?,?) ON CONFLICT(id) DO NOTHING`, session.ID, userID, string(raw), session.CreatedAt, nowString()); err != nil {
			return err
		}
	}
	return tx.Commit()
}
