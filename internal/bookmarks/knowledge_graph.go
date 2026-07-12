package bookmarks

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"sort"
	"strings"

	"github.com/glnarayanan/arivu/internal/auth"
	"github.com/glnarayanan/arivu/internal/providers"
)

type graphV2Node struct {
	ID        string `json:"id"`
	Type      string `json:"type"`
	SourceID  string `json:"source_id"`
	Title     string `json:"title"`
	Summary   string `json:"summary"`
	Source    string `json:"source"`
	UpdatedAt string `json:"updated_at"`
}

type graphV2Edge struct {
	ID         string  `json:"id"`
	Type       string  `json:"type"`
	From       string  `json:"from"`
	To         string  `json:"to"`
	Provenance string  `json:"provenance"`
	Confidence float64 `json:"confidence"`
}

func (s *Service) KnowledgeGraphV2(w http.ResponseWriter, r *http.Request, user auth.User) {
	nodeLimit := queryInt(r, "node_limit", 80, 1, 200)
	edgeLimit := queryInt(r, "edge_limit", 200, 1, 500)
	depth := queryInt(r, "depth", 1, 0, 2)
	focusType, focusID, hasFocus := splitGraphNodeID(strings.TrimSpace(r.URL.Query().Get("focus")))

	nodes, err := s.graphV2Nodes(r.Context(), user.ID, nodeLimit, focusType, focusID, hasFocus)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Could not load knowledge graph")
		return
	}
	if hasFocus && depth > 0 {
		nodes = s.addFocusedNeighbors(r.Context(), user.ID, nodes, focusType, focusID, depth, nodeLimit)
	}
	edges := s.graphV2Edges(r.Context(), user.ID, nodes, edgeLimit, true)
	nodeFacets := map[string]int{}
	edgeFacets := map[string]int{}
	for _, node := range nodes {
		nodeFacets[node.Type]++
	}
	for _, edge := range edges {
		edgeFacets[edge.Type]++
	}
	truncated := len(nodes) >= nodeLimit || len(edges) >= edgeLimit
	writeJSON(w, http.StatusOK, map[string]any{"nodes": nodes, "edges": edges, "facets": map[string]any{"node_types": nodeFacets, "edge_types": edgeFacets}, "truncated": truncated})
}

func (s *Service) graphV2Nodes(ctx context.Context, userID string, limit int, focusType, focusID string, hasFocus bool) ([]graphV2Node, error) {
	if hasFocus {
		if focus, ok := s.graphV2Node(ctx, userID, focusType, focusID); ok {
			return []graphV2Node{focus}, nil
		}
		return []graphV2Node{}, nil
	}
	rows, err := s.db.QueryContext(ctx, libraryUnion+" WHERE user_id=? AND "+graphNodeEligibilitySQL("library")+" ORDER BY updated_at DESC,item_type,id LIMIT ?", userID, limit)
	if err != nil {
		return nil, err
	}
	nodes, err := scanGraphV2Nodes(rows)
	if err != nil {
		return nil, err
	}
	return nodes, nil
}

func scanGraphV2Nodes(rows interface {
	Next() bool
	Scan(...any) error
	Err() error
	Close() error
}) ([]graphV2Node, error) {
	defer rows.Close()
	nodes := []graphV2Node{}
	for rows.Next() {
		var userID, id, itemType, title, body, source, stage, topic, connection, created, updated string
		if err := rows.Scan(&userID, &id, &itemType, &title, &body, &source, &stage, &topic, &connection, &created, &updated); err != nil {
			return nil, err
		}
		nodes = append(nodes, graphV2Node{ID: graphNodeID(itemType, id), Type: itemType, SourceID: id, Title: title, Summary: truncateText(body, 240), Source: source, UpdatedAt: updated})
	}
	return nodes, rows.Err()
}

func (s *Service) graphV2Node(ctx context.Context, userID, itemType, id string) (graphV2Node, bool) {
	rows, err := s.db.QueryContext(ctx, libraryUnion+" WHERE user_id=? AND item_type=? AND id=? AND "+graphNodeEligibilitySQL("library")+" LIMIT 1", userID, itemType, id)
	if err != nil {
		return graphV2Node{}, false
	}
	nodes, err := scanGraphV2Nodes(rows)
	return firstGraphNode(nodes, err)
}

// graphNodeEligibilitySQL keeps raw bookmarks in the Library while admitting
// them to the Graph only after usable evidence exists or the user has made an
// intentional connection to another knowledge object.
func graphNodeEligibilitySQL(alias string) string {
	bookmark := "graph_bookmark"
	return "(" + alias + ".item_type<>'bookmark' OR EXISTS (SELECT 1 FROM bookmarks " + bookmark +
		" WHERE " + bookmark + ".id=" + alias + ".id AND " + bookmark + ".user_id=" + alias + ".user_id AND ((" +
		bookmark + ".summary_version='" + providers.SummaryPromptVersion + "' AND " + bookmark + ".enrichment_version='" + providers.SemanticVersion +
		"' AND EXISTS (SELECT 1 FROM bookmark_evidence graph_evidence WHERE graph_evidence.bookmark_id=" + bookmark + ".id AND graph_evidence.user_id=" + bookmark +
		".user_id AND graph_evidence.is_selected=1 AND graph_evidence.quality_status='complete' AND trim(COALESCE(graph_evidence.content_text,''))<>'')) OR " +
		"EXISTS (SELECT 1 FROM item_links graph_link WHERE graph_link.user_id=" + bookmark + ".user_id AND ((graph_link.from_type='bookmark' AND graph_link.from_id=" + bookmark +
		".id) OR (graph_link.to_type='bookmark' AND graph_link.to_id=" + bookmark + ".id))) OR " +
		"EXISTS (SELECT 1 FROM bookmark_notes graph_note WHERE graph_note.user_id=" + bookmark + ".user_id AND graph_note.bookmark_id=" + bookmark + ".id) OR " +
		"EXISTS (SELECT 1 FROM annotations graph_annotation WHERE graph_annotation.user_id=" + bookmark + ".user_id AND graph_annotation.bookmark_id=" + bookmark + ".id) OR " +
		"EXISTS (SELECT 1 FROM knowledge_objects graph_object WHERE graph_object.user_id=" + bookmark + ".user_id AND graph_object.source_item_type='bookmark' AND graph_object.source_item_id=" + bookmark + ".id))))"
}

func firstGraphNode(nodes []graphV2Node, err error) (graphV2Node, bool) {
	if err != nil || len(nodes) == 0 {
		return graphV2Node{}, false
	}
	return nodes[0], true
}

func (s *Service) addFocusedNeighbors(ctx context.Context, userID string, nodes []graphV2Node, focusType, focusID string, depth, limit int) []graphV2Node {
	frontier := []string{graphNodeID(focusType, focusID)}
	seen := map[string]bool{}
	for _, node := range nodes {
		seen[node.ID] = true
	}
	for level := 0; level < depth && len(frontier) > 0 && len(nodes) < limit; level++ {
		next := []string{}
		for _, current := range frontier {
			itemType, itemID, ok := splitGraphNodeID(current)
			if !ok {
				continue
			}
			for _, endpoint := range s.graphNeighborRefs(ctx, userID, itemType, itemID) {
				nodeID := graphNodeID(endpoint[0], endpoint[1])
				if seen[nodeID] || len(nodes) >= limit {
					continue
				}
				if node, ok := s.graphV2Node(ctx, userID, endpoint[0], endpoint[1]); ok {
					nodes = append(nodes, node)
					seen[nodeID] = true
					next = append(next, nodeID)
				}
			}
		}
		frontier = next
	}
	return nodes
}

func (s *Service) graphNeighborRefs(ctx context.Context, userID, itemType, itemID string) [][2]string {
	refs := [][2]string{}
	if itemType == "bookmark" || itemType == "note" {
		rows, err := s.db.QueryContext(ctx, `SELECT from_type,from_id,to_type,to_id FROM item_links WHERE user_id=? AND ((from_type=? AND from_id=?) OR (to_type=? AND to_id=?)) ORDER BY created_at,id`, userID, itemType, itemID, itemType, itemID)
		if err == nil {
			for rows.Next() {
				var fromType, fromID, toType, toID string
				_ = rows.Scan(&fromType, &fromID, &toType, &toID)
				refs = append(refs, [2]string{fromType, fromID}, [2]string{toType, toID})
			}
			rows.Close()
		}
	}
	if itemType == "bookmark" {
		rows, err := s.db.QueryContext(ctx, `SELECT note_id FROM bookmark_notes WHERE user_id=? AND bookmark_id=? ORDER BY note_id`, userID, itemID)
		if err == nil {
			for rows.Next() {
				var noteID string
				_ = rows.Scan(&noteID)
				refs = append(refs, [2]string{"note", noteID})
			}
			rows.Close()
		}
		for _, term := range []struct{ table, column, itemType string }{{"bookmark_concepts", "concept", "concept"}, {"bookmark_entities", "entity", "entity"}} {
			rows, err := s.db.QueryContext(ctx, `SELECT t.`+term.column+` FROM `+term.table+` t WHERE t.user_id=? AND t.bookmark_id=? AND `+semanticEligibilitySQL("t")+` ORDER BY t.`+term.column, userID, itemID)
			if err == nil {
				for rows.Next() {
					var value string
					_ = rows.Scan(&value)
					refs = append(refs, [2]string{term.itemType, value})
				}
				rows.Close()
			}
		}
	}
	if itemType == "note" {
		rows, err := s.db.QueryContext(ctx, `SELECT bookmark_id FROM bookmark_notes WHERE user_id=? AND note_id=? ORDER BY bookmark_id`, userID, itemID)
		if err == nil {
			for rows.Next() {
				var bookmarkID string
				_ = rows.Scan(&bookmarkID)
				refs = append(refs, [2]string{"bookmark", bookmarkID})
			}
			rows.Close()
		}
	}
	if itemType == "concept" || itemType == "entity" {
		table, column := "bookmark_concepts", "concept"
		if itemType == "entity" {
			table, column = "bookmark_entities", "entity"
		}
		rows, err := s.db.QueryContext(ctx, `SELECT t.bookmark_id FROM `+table+` t WHERE t.user_id=? AND t.`+column+`=? AND `+semanticEligibilitySQL("t")+` ORDER BY t.bookmark_id`, userID, itemID)
		if err == nil {
			for rows.Next() {
				var bookmarkID string
				_ = rows.Scan(&bookmarkID)
				refs = append(refs, [2]string{"bookmark", bookmarkID})
			}
			rows.Close()
		}
	}
	if itemType == "annotation" {
		var bookmarkID string
		if s.db.QueryRowContext(ctx, `SELECT bookmark_id FROM annotations WHERE user_id=? AND id=?`, userID, itemID).Scan(&bookmarkID) == nil {
			refs = append(refs, [2]string{"bookmark", bookmarkID})
		}
	}
	if itemType == "knowledge_object" {
		var sourceType, sourceID string
		if s.db.QueryRowContext(ctx, `SELECT source_item_type,source_item_id FROM knowledge_objects WHERE user_id=? AND id=?`, userID, itemID).Scan(&sourceType, &sourceID) == nil && sourceID != "" {
			refs = append(refs, [2]string{graphSourceType(sourceType), sourceID})
		}
	}
	return refs
}

func (s *Service) graphV2Edges(ctx context.Context, userID string, nodes []graphV2Node, limit int, hideFeedback bool) []graphV2Edge {
	known := map[string]bool{}
	for _, node := range nodes {
		known[node.ID] = true
	}
	hidden := map[string]bool{}
	if hideFeedback {
		hidden = s.hiddenKnowledgeTargets(ctx, userID, "relationship")
	}
	edges := []graphV2Edge{}
	rows, err := s.db.QueryContext(ctx, `SELECT from_type,from_id,to_type,to_id,source FROM item_links WHERE user_id=? ORDER BY created_at,id`, userID)
	if err == nil {
		for rows.Next() && len(edges) < limit {
			var fromType, fromID, toType, toID, source string
			_ = rows.Scan(&fromType, &fromID, &toType, &toID, &source)
			from, to := graphNodeID(fromType, fromID), graphNodeID(toType, toID)
			if known[from] && known[to] {
				edge := newGraphV2Edge("explicit", from, to, firstNonEmpty(source, "manual"), 1)
				if !hidden[edge.ID] {
					edges = append(edges, edge)
				}
			}
		}
		rows.Close()
	}
	rows, err = s.db.QueryContext(ctx, `SELECT bookmark_id,note_id FROM bookmark_notes WHERE user_id=? ORDER BY bookmark_id,note_id`, userID)
	if err == nil {
		for rows.Next() && len(edges) < limit {
			var bookmarkID, noteID string
			_ = rows.Scan(&bookmarkID, &noteID)
			from, to := graphNodeID("bookmark", bookmarkID), graphNodeID("note", noteID)
			if known[from] && known[to] {
				edge := newGraphV2Edge("explicit", from, to, "bookmark_notes", 1)
				if !hidden[edge.ID] {
					edges = append(edges, edge)
				}
			}
		}
		rows.Close()
	}
	for _, table := range []struct{ table, column, kind, nodeType string }{{"bookmark_concepts", "concept", "shared_concept", "concept"}, {"bookmark_entities", "entity", "shared_entity", "entity"}} {
		query := `SELECT t.bookmark_id,t.` + table.column + ` FROM ` + table.table + ` t WHERE t.user_id=? AND ` + semanticEligibilitySQL("t") + ` ORDER BY t.` + table.column + `,t.bookmark_id`
		rows, err := s.db.QueryContext(ctx, query, userID)
		if err != nil {
			continue
		}
		for rows.Next() && len(edges) < limit {
			var bookmarkID, term string
			_ = rows.Scan(&bookmarkID, &term)
			from, to := graphNodeID("bookmark", bookmarkID), graphNodeID(table.nodeType, term)
			if known[from] && known[to] {
				edge := newGraphV2Edge(table.kind, from, to, table.table, 0.9)
				if !hidden[edge.ID] {
					edges = append(edges, edge)
				}
			}
		}
		rows.Close()
	}
	type embeddedNode struct {
		id        string
		embedding []float64
	}
	embedded := []embeddedNode{}
	rows, err = s.db.QueryContext(ctx, `SELECT b.id,b.embedding FROM bookmarks b WHERE b.user_id=? AND b.embedding IS NOT NULL AND b.enrichment_version=? AND EXISTS (SELECT 1 FROM bookmark_evidence e WHERE e.bookmark_id=b.id AND e.user_id=b.user_id AND e.is_selected=1 AND e.quality_status='complete') ORDER BY b.id`, userID, providers.SemanticVersion)
	if err == nil {
		for rows.Next() {
			var id string
			var raw []byte
			_ = rows.Scan(&id, &raw)
			if known[graphNodeID("bookmark", id)] {
				embedded = append(embedded, embeddedNode{id: id, embedding: parseEmbedding(raw)})
			}
		}
		rows.Close()
	}
	for i := 0; i < len(embedded) && len(edges) < limit; i++ {
		for j := i + 1; j < len(embedded) && len(edges) < limit; j++ {
			confidence := cosineSimilarity(embedded[i].embedding, embedded[j].embedding)
			if confidence < 0.82 {
				continue
			}
			edge := newGraphV2Edge("semantic_similarity", graphNodeID("bookmark", embedded[i].id), graphNodeID("bookmark", embedded[j].id), "stored_embeddings", roundFloat(confidence, 4))
			if !hidden[edge.ID] {
				edges = append(edges, edge)
			}
		}
	}
	rows, err = s.db.QueryContext(ctx, `SELECT id,source_item_type,source_item_id FROM knowledge_objects WHERE user_id=? AND source_item_id<>'' ORDER BY id`, userID)
	if err == nil {
		for rows.Next() && len(edges) < limit {
			var id, sourceType, sourceID string
			_ = rows.Scan(&id, &sourceType, &sourceID)
			from, to := graphNodeID("knowledge_object", id), graphNodeID(graphSourceType(sourceType), sourceID)
			if known[from] && known[to] {
				edge := newGraphV2Edge("source", from, to, "knowledge_objects.source_item_id", 1)
				if !hidden[edge.ID] {
					edges = append(edges, edge)
				}
			}
		}
		rows.Close()
	}
	rows, err = s.db.QueryContext(ctx, `SELECT id,bookmark_id FROM annotations WHERE user_id=? ORDER BY created_at,id`, userID)
	if err == nil {
		for rows.Next() && len(edges) < limit {
			var id, bookmarkID string
			_ = rows.Scan(&id, &bookmarkID)
			from, to := graphNodeID("annotation", id), graphNodeID("bookmark", bookmarkID)
			if known[from] && known[to] {
				edge := newGraphV2Edge("source", from, to, "annotations.bookmark_id", 1)
				if !hidden[edge.ID] {
					edges = append(edges, edge)
				}
			}
		}
		rows.Close()
	}
	sort.Slice(edges, func(i, j int) bool { return edges[i].ID < edges[j].ID })
	return edges
}

func graphSourceType(itemType string) string {
	if itemType == "object" {
		return "knowledge_object"
	}
	return itemType
}

func newGraphV2Edge(kind, from, to, provenance string, confidence float64) graphV2Edge {
	return graphV2Edge{ID: stableKnowledgeID("edge", kind, from, to), Type: kind, From: from, To: to, Provenance: provenance, Confidence: confidence}
}

func stableKnowledgeID(parts ...string) string {
	sum := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return parts[0] + "_" + hex.EncodeToString(sum[:12])
}

func graphNodeID(itemType, id string) string { return itemType + ":" + id }

func splitGraphNodeID(raw string) (string, string, bool) {
	itemType, id, ok := strings.Cut(raw, ":")
	return itemType, id, ok && itemType != "" && id != ""
}

func hasGraphNode(nodes []graphV2Node, id string) bool {
	for _, node := range nodes {
		if node.ID == id {
			return true
		}
	}
	return false
}

func truncateText(value string, max int) string {
	value = strings.TrimSpace(value)
	if len(value) <= max {
		return value
	}
	return value[:max] + "..."
}
