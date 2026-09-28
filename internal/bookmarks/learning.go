package bookmarks

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"unicode"

	"github.com/glnarayanan/arivu/internal/auth"
	"github.com/glnarayanan/arivu/internal/ids"
	"github.com/glnarayanan/arivu/internal/providers"
)

type learningSource struct {
	Type string `json:"type"`
	ID   string `json:"id"`
}

type learningPassage struct {
	providers.LearningPassage
	Source learningSource `json:"source"`
	Title  string         `json:"title"`
	URL    string         `json:"url"`
	Hash   string         `json:"hash"`
}

type learningExchange struct {
	Question string                   `json:"question"`
	Answer   providers.LearningAnswer `json:"answer"`
	Provider string                   `json:"provider"`
}

type learningSession struct {
	ID        string                       `json:"id"`
	Kind      string                       `json:"kind"`
	Question  string                       `json:"question"`
	Passages  []learningPassage            `json:"passages"`
	Exchanges []learningExchange           `json:"exchanges"`
	Questions []providers.LearningQuestion `json:"questions"`
	Choices   []int                        `json:"choices"`
	Revision  int                          `json:"revision"`
	CreatedAt string                       `json:"created_at"`
}

// Only this redacted destination, never credentials or a URL query, reaches UI.
func learningDestination(c providers.GeminiClient) map[string]any {
	definition := providers.ModelProviderDefinition(c.Provider)
	base, model := fallback(strings.TrimSpace(c.BaseURL), definition.BaseURL), fallback(strings.TrimSpace(c.Model), definition.DefaultModel)
	parsed, err := url.Parse(base)
	available := err == nil && parsed.Host != "" && model != "" && (c.APIKey != "" || definition.APIKeyOptional)
	host := ""
	if err == nil {
		host = parsed.Host
	}
	return map[string]any{"name": definition.Name, "host": host, "model": model, "available": available, "consent": fmt.Sprintf("%x", sha256.Sum256([]byte(definition.ID+"\x00"+base+"\x00"+model)))}
}

func (s *Service) PrepareLearning(w http.ResponseWriter, r *http.Request, user auth.User) {
	var input struct {
		Kind     string           `json:"kind"`
		Question string           `json:"question"`
		Sources  []learningSource `json:"sources"`
	}
	if decodeJSON(r, &input) != nil || (input.Kind != "chat" && input.Kind != "quiz") || len(input.Sources) > 8 || len(input.Question) > 2000 {
		writeError(w, 400, "Choose chat or quiz, up to eight sources, and a question under 2000 bytes")
		return
	}
	input.Question = strings.TrimSpace(input.Question)
	libraryScope := len(input.Sources) == 0
	if len(input.Sources) == 0 {
		if len(input.Question) < 2 {
			writeError(w, 400, "Enter a topic to find sources in your library")
			return
		}
		{
			// Full questions rarely appear verbatim. One bounded query retrieves
			// candidates by word overlap; only original text is sent to the model.
			var scores []string
			var args []any
			for _, term := range strings.FieldsFunc(strings.ToLower(input.Question), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsNumber(r) }) {
				if len(term) < 3 || strings.Contains(" how what where when which does are the and for with can why ", " "+term+" ") {
					continue
				}
				scores = append(scores, "(CASE WHEN instr(lower(title||' '||body),?)>0 THEN 1 ELSE 0 END)")
				args = append(args, term)
				if len(scores) == 8 {
					break
				}
			}
			if len(scores) > 0 {
				args = append(args, user.ID)
				rows, err := s.db.QueryContext(r.Context(), `SELECT item_type,item_id FROM (SELECT item_type,item_id,updated_at,`+strings.Join(scores, "+")+` score FROM search_index si WHERE user_id=? AND ((item_type='note' AND EXISTS(SELECT 1 FROM notes n WHERE n.id=si.item_id AND n.user_id=si.user_id AND n.source NOT LIKE 'ai:%' AND trim(n.body)<>'')) OR (item_type='bookmark' AND EXISTS(SELECT 1 FROM bookmark_evidence e WHERE e.bookmark_id=si.item_id AND e.user_id=si.user_id AND e.is_selected=1 AND e.quality_status NOT IN ('failed','metadata_only') AND trim(e.content_text)<>'')))) WHERE score>0 ORDER BY score DESC,updated_at DESC LIMIT 8`, args...)
				if err != nil {
					writeError(w, 500, "Could not find sources")
					return
				}
				for rows.Next() {
					var source learningSource
					if err = rows.Scan(&source.Type, &source.ID); err != nil {
						break
					}
					input.Sources = append(input.Sources, source)
				}
				rowErr := rows.Err()
				rows.Close()
				if err != nil || rowErr != nil {
					writeError(w, 500, "Could not find sources")
					return
				}
			}
		}
	}
	session := learningSession{ID: ids.New(), Kind: input.Kind, Question: input.Question, CreatedAt: nowString()}
	for _, source := range input.Sources {
		title, link, content, err := s.learningSourceText(r.Context(), user.ID, source)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				if libraryScope {
					continue
				}
				writeError(w, 404, "Source is unavailable or has no original text")
				return
			}
			writeError(w, 500, "Could not read source")
			return
		}
		limit := 16 / max(1, len(input.Sources))
		for _, text := range learningChunks(content, input.Question, limit) {
			passage := learningPassage{LearningPassage: providers.LearningPassage{ID: fmt.Sprintf("p%d", len(session.Passages)+1), Text: text}, Source: source, Title: title, URL: link, Hash: fmt.Sprintf("%x", sha256.Sum256([]byte(content)))}
			session.Passages = append(session.Passages, passage)
		}
	}
	if len(session.Passages) == 0 {
		writeError(w, 422, "No original passages matched. Try a source title or select sources in Library.")
		return
	}
	raw, _ := json.Marshal(session)
	if _, err := s.db.ExecContext(r.Context(), `INSERT INTO learning_sessions(id,user_id,payload_json,created_at,updated_at) VALUES(?,?,?,?,?)`, session.ID, user.ID, string(raw), session.CreatedAt, session.CreatedAt); err != nil {
		writeError(w, 500, "Could not save source preview")
		return
	}
	s.writeLearning(w, r, user, session)
}

func (s *Service) learningSourceText(ctx context.Context, userID string, source learningSource) (title, link, text string, err error) {
	switch source.Type {
	case "bookmark":
		err = s.db.QueryRowContext(ctx, `SELECT COALESCE(b.title,b.url),b.url,substr(e.content_text,1,120000) FROM bookmarks b JOIN bookmark_evidence e ON e.bookmark_id=b.id AND e.user_id=b.user_id WHERE b.id=? AND b.user_id=? AND e.is_selected=1 AND e.quality_status NOT IN ('failed','metadata_only') AND trim(e.content_text)<>''`, source.ID, userID).Scan(&title, &link, &text)
	case "note":
		err = s.db.QueryRowContext(ctx, `SELECT title,substr(body,1,120000) FROM notes WHERE id=? AND user_id=? AND source NOT LIKE 'ai:%' AND trim(body)<>''`, source.ID, userID).Scan(&title, &text)
	default:
		err = sql.ErrNoRows
	}
	return
}

// Whole UTF-8 spans remain exact substrings. Rank locally; never use summaries
// as evidence. 16 passages of at most 1500 bytes keep the provider input bounded.
func learningChunks(content, question string, limit int) []string {
	type chunk struct {
		text  string
		score int
	}
	var chunks []chunk
	terms := strings.FieldsFunc(strings.ToLower(question), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsNumber(r) })
	for len(content) > 0 {
		end := min(1500, len(content))
		for end < len(content) && content[end]&0xc0 == 0x80 {
			end--
		}
		text := content[:end]
		content = content[end:]
		if strings.TrimSpace(text) == "" {
			continue
		}
		score := 0
		for _, term := range terms {
			if len(term) > 2 {
				score += strings.Count(strings.ToLower(text), term)
			}
		}
		chunks = append(chunks, chunk{text, score})
	}
	sort.SliceStable(chunks, func(i, j int) bool { return chunks[i].score > chunks[j].score })
	result := []string{}
	for _, chunk := range chunks[:min(limit, len(chunks))] {
		result = append(result, chunk.text)
	}
	return result
}

func (s *Service) loadLearning(ctx context.Context, userID, id string) (learningSession, error) {
	var session learningSession
	var raw string
	var revision int
	err := s.db.QueryRowContext(ctx, `SELECT payload_json,revision FROM learning_sessions WHERE id=? AND user_id=?`, id, userID).Scan(&raw, &revision)
	if err == nil {
		err = json.Unmarshal([]byte(raw), &session)
	}
	session.ID = id
	session.Revision = revision
	return session, err
}

func (s *Service) Learning(w http.ResponseWriter, r *http.Request, user auth.User) {
	session, err := s.loadLearning(r.Context(), user.ID, r.PathValue("id"))
	if err != nil {
		writeError(w, 404, "Conversation or quiz not found")
		return
	}
	s.writeLearning(w, r, user, session)
}

func (s *Service) LearningList(w http.ResponseWriter, r *http.Request, user auth.User) {
	rows, err := s.db.QueryContext(r.Context(), `SELECT id,json_extract(payload_json,'$.kind'),json_extract(payload_json,'$.question'),updated_at FROM learning_sessions WHERE user_id=? ORDER BY updated_at DESC LIMIT 50`, user.ID)
	if err != nil {
		writeError(w, 500, "Could not load conversations")
		return
	}
	defer rows.Close()
	items := []map[string]any{}
	for rows.Next() {
		var id, kind, question, updated string
		if rows.Scan(&id, &kind, &question, &updated) != nil {
			writeError(w, 500, "Could not load conversations")
			return
		}
		items = append(items, map[string]any{"id": id, "kind": kind, "question": question, "updated_at": updated})
	}
	if rows.Err() != nil {
		writeError(w, 500, "Could not load conversations")
		return
	}
	writeJSON(w, 200, map[string]any{"items": items, "provider": learningDestination(s.aiClient(r.Context()))})
}

func (s *Service) writeLearning(w http.ResponseWriter, r *http.Request, user auth.User, session learningSession) {
	// Questions are deliberately projected, never serialized with hidden answers.
	questions := []map[string]any{}
	for i, q := range session.Questions {
		item := map[string]any{"question": q.Question, "options": q.Options}
		if len(session.Choices) == len(session.Questions) {
			item["correct"] = q.Correct
			item["choice"] = session.Choices[i]
			item["explanation"] = q.Explanation
			item["citations"] = q.Citations
		}
		questions = append(questions, item)
	}
	writeJSON(w, 200, map[string]any{"id": session.ID, "kind": session.Kind, "question": session.Question, "passages": session.Passages, "exchanges": session.Exchanges, "questions": questions, "revision": session.Revision, "provider": learningDestination(s.aiClient(r.Context()))})
}

func (s *Service) GenerateLearning(w http.ResponseWriter, r *http.Request, user auth.User) {
	var input struct {
		Question string `json:"question"`
		Consent  string `json:"consent"`
		Revision int    `json:"revision"`
	}
	if decodeJSON(r, &input) != nil || len(strings.TrimSpace(input.Question)) == 0 || len(input.Question) > 2000 {
		writeError(w, 400, "Enter a question under 2000 bytes")
		return
	}
	session, err := s.loadLearning(r.Context(), user.ID, r.PathValue("id"))
	if err != nil {
		writeError(w, 404, "Conversation or quiz not found")
		return
	}
	if input.Revision != session.Revision || len(session.Exchanges) >= 6 || len(session.Questions) > 0 {
		writeError(w, 409, "This session changed or is complete. Reload it or start a new one.")
		return
	}
	client := s.aiClient(r.Context())
	destination := learningDestination(client)
	if destination["available"] != true {
		writeError(w, 503, "No AI provider is configured. Reading, notes, and search still work.")
		return
	}
	if input.Consent != destination["consent"] {
		writeError(w, 409, "Review the current provider destination before sending")
		return
	}
	passages := make([]providers.LearningPassage, 0, len(session.Passages))
	checked := map[learningSource]string{}
	for _, passage := range session.Passages {
		text, ok := checked[passage.Source]
		if !ok {
			_, _, text, err = s.learningSourceText(r.Context(), user.ID, passage.Source)
			if err != nil {
				writeError(w, 409, "A source is no longer available. Start a new session.")
				return
			}
			checked[passage.Source] = text
		}
		if fmt.Sprintf("%x", sha256.Sum256([]byte(text))) != passage.Hash || !strings.Contains(text, passage.Text) {
			writeError(w, 409, "A source changed since this preview. Start a new session to review its current text.")
			return
		}
		passages = append(passages, passage.LearningPassage)
	}
	if session.Kind == "quiz" {
		session.Questions, err = client.QuizKnowledge(r.Context(), passages)
	} else {
		history := []providers.LearningTurn{}
		for _, exchange := range session.Exchanges {
			history = append(history, providers.LearningTurn{Role: "user", Text: exchange.Question})
			text := "Not enough evidence."
			if !exchange.Answer.Insufficient {
				var parts []string
				for _, claim := range exchange.Answer.Claims {
					parts = append(parts, claim.Text)
				}
				text = strings.Join(parts, "\n")
			}
			history = append(history, providers.LearningTurn{Role: "assistant", Text: text})
		}
		// Keep recent full turns within the provider history budget.
		for len(history) > 0 {
			total := 0
			for _, turn := range history {
				total += len(turn.Text)
			}
			if total <= 12000 {
				break
			}
			history = history[2:]
		}
		var answer providers.LearningAnswer
		answer, err = client.AnswerKnowledge(r.Context(), input.Question, history, passages)
		if err == nil {
			session.Exchanges = append(session.Exchanges, learningExchange{Question: input.Question, Answer: answer, Provider: fmt.Sprint(destination["name"], " · ", destination["host"], " · ", destination["model"])})
		}
	}
	if err != nil {
		writeError(w, 502, "The provider could not return a valid source-backed response. Your sources are unchanged; you can retry.")
		return
	}
	if err = s.saveLearning(r.Context(), user.ID, &session); err != nil {
		writeError(w, 409, "The session changed while generating. Reload before retrying.")
		return
	}
	s.writeLearning(w, r, user, session)
}

func (s *Service) saveLearning(ctx context.Context, userID string, session *learningSession) error {
	raw, err := json.Marshal(session)
	if err != nil {
		return err
	}
	result, err := s.db.ExecContext(ctx, `UPDATE learning_sessions SET payload_json=?,revision=revision+1,updated_at=? WHERE id=? AND user_id=? AND revision=?`, string(raw), nowString(), session.ID, userID, session.Revision)
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count != 1 {
		return errors.New("session changed")
	}
	session.Revision++
	return nil
}

func (s *Service) SubmitLearningQuiz(w http.ResponseWriter, r *http.Request, user auth.User) {
	var input struct {
		Choices  []int `json:"choices"`
		Revision int   `json:"revision"`
	}
	if decodeJSON(r, &input) != nil {
		writeError(w, 400, "Choose one answer per question")
		return
	}
	session, err := s.loadLearning(r.Context(), user.ID, r.PathValue("id"))
	if err != nil {
		writeError(w, 404, "Quiz not found")
		return
	}
	if session.Kind != "quiz" || len(session.Questions) != 3 || len(session.Choices) > 0 || input.Revision != session.Revision {
		writeError(w, 409, "This quiz changed or has already been submitted")
		return
	}
	if len(input.Choices) != 3 {
		writeError(w, 400, "Answer all three questions")
		return
	}
	for _, choice := range input.Choices {
		if choice < 0 || choice > 3 {
			writeError(w, 400, "Invalid answer choice")
			return
		}
	}
	session.Choices = input.Choices
	if s.saveLearning(r.Context(), user.ID, &session) != nil {
		writeError(w, 409, "Quiz changed; reload it")
		return
	}
	s.writeLearning(w, r, user, session)
}

func (s *Service) SaveLearningNote(w http.ResponseWriter, r *http.Request, user auth.User) {
	var input struct {
		Exchange *int   `json:"exchange"`
		Passage  string `json:"passage"`
	}
	if decodeJSON(r, &input) != nil || (input.Exchange == nil) == (input.Passage == "") {
		writeError(w, 400, "Choose an answer or passage to save")
		return
	}
	session, err := s.loadLearning(r.Context(), user.ID, r.PathValue("id"))
	if err != nil {
		writeError(w, 404, "Session not found")
		return
	}
	title, body, source := "Saved passage", "", "quote"
	var citations []providers.LearningCitation
	if input.Exchange != nil {
		index := *input.Exchange
		if index < 0 || index >= len(session.Exchanges) || session.Exchanges[index].Answer.Insufficient {
			writeError(w, 400, "No supported answer to save")
			return
		}
		exchange := session.Exchanges[index]
		title = exchange.Question
		source = "ai:answer"
		body = "AI-generated answer · " + exchange.Provider + "\n\n"
		for _, claim := range exchange.Answer.Claims {
			body += claim.Text + "\n\n"
			citations = append(citations, claim.Citations...)
		}
	} else {
		for _, passage := range session.Passages {
			if passage.ID == input.Passage {
				title = passage.Title
				citations = append(citations, providers.LearningCitation{PassageID: passage.ID, Quote: passage.Text})
			}
		}
		if len(citations) == 0 {
			writeError(w, 400, "Passage not found")
			return
		}
	}
	links := map[learningSource]bool{}
	body += "Source evidence saved " + nowString() + ":\n"
	seen := map[string]bool{}
	for _, citation := range citations {
		key := citation.PassageID + "\x00" + citation.Quote
		if seen[key] {
			continue
		}
		seen[key] = true
		for _, passage := range session.Passages {
			if passage.ID == citation.PassageID {
				body += "\n" + passage.Title + "\n" + passage.URL + "\n> " + strings.ReplaceAll(citation.Quote, "\n", "\n> ") + "\n"
				links[passage.Source] = true
			}
		}
	}
	tx, err := s.db.BeginTx(r.Context(), nil)
	if err != nil {
		writeError(w, 500, "Could not save note")
		return
	}
	defer tx.Rollback()
	id, now := ids.New(), nowString()
	_, err = tx.ExecContext(r.Context(), `INSERT INTO notes(id,user_id,title,body,source,created_at,updated_at) VALUES(?,?,?,?,?,?,?)`, id, user.ID, title, body, source, now, now)
	if err == nil {
		for link := range links {
			table := "bookmarks"
			if link.Type == "note" {
				table = "notes"
			}
			_, err = tx.ExecContext(r.Context(), `INSERT INTO item_links(id,user_id,from_type,from_id,to_type,to_id,label,source,created_at) SELECT ?,?,'note',?,?,?,'Source evidence','learning',? WHERE EXISTS(SELECT 1 FROM `+table+` WHERE id=? AND user_id=?)`, ids.New(), user.ID, id, link.Type, link.ID, now, link.ID, user.ID)
			if err != nil {
				break
			}
		}
	}
	if err != nil || tx.Commit() != nil {
		writeError(w, 500, "Could not save note")
		return
	}
	s.refreshSearchIndex(r.Context(), user.ID)
	writeJSON(w, 201, map[string]any{"id": id})
}

func (s *Service) DeleteLearning(w http.ResponseWriter, r *http.Request, user auth.User) {
	result, err := s.db.ExecContext(r.Context(), `DELETE FROM learning_sessions WHERE id=? AND user_id=?`, r.PathValue("id"), user.ID)
	if err != nil {
		writeError(w, 500, "Could not delete session")
		return
	}
	count, _ := result.RowsAffected()
	if count == 0 {
		writeError(w, 404, "Session not found")
		return
	}
	writeJSON(w, 200, map[string]string{"message": "Session deleted; saved notes remain"})
}
