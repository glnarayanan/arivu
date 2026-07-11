package providers

import (
	"io"
	"net/http"
	"strings"
	"testing"
)

type xRoundTripFunc func(*http.Request) (*http.Response, error)

func (fn xRoundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) { return fn(req) }

func TestBookmarkPageRequestsAndRetainsSourceContext(t *testing.T) {
	client := XClient{
		AccessToken: "token",
		APIBaseURL:  "https://x.example.test",
		HTTP: &http.Client{Transport: xRoundTripFunc(func(req *http.Request) (*http.Response, error) {
			query := req.URL.Query()
			for _, field := range []string{"created_at", "note_tweet", "referenced_tweets", "attachments"} {
				if !strings.Contains(query.Get("tweet.fields"), field) {
					t.Fatalf("tweet.fields missing %q: %q", field, query.Get("tweet.fields"))
				}
			}
			for _, expansion := range []string{"attachments.media_keys", "referenced_tweets.id", "referenced_tweets.id.author_id"} {
				if !strings.Contains(query.Get("expansions"), expansion) {
					t.Fatalf("expansions missing %q: %q", expansion, query.Get("expansions"))
				}
			}
			if !strings.Contains(query.Get("media.fields"), "alt_text") {
				t.Fatalf("media.fields = %q", query.Get("media.fields"))
			}
			body := `{"data":[{"id":"post-1","text":"truncated","author_id":"author-1","created_at":"2026-07-10T04:00:00Z","note_tweet":{"text":"complete long post"},"referenced_tweets":[{"type":"quoted","id":"post-2"}],"attachments":{"media_keys":["media-1"]}}],"includes":{"users":[{"id":"author-1","username":"author","name":"Author"}],"tweets":[{"id":"post-2","text":"quoted context","author_id":"author-2"}],"media":[{"media_key":"media-1","type":"photo","alt_text":"A useful diagram"}]},"meta":{"next_token":"next"}}`
			return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}, nil
		})},
	}

	page, err := client.BookmarkPage(t.Context(), "user-1", "", 100)
	if err != nil {
		t.Fatalf("BookmarkPage() error = %v", err)
	}
	if len(page.Bookmarks) != 1 || page.Bookmarks[0].EvidenceText() != "complete long post" || page.Bookmarks[0].CreatedAt != "2026-07-10T04:00:00Z" {
		t.Fatalf("bookmark source context = %#v", page.Bookmarks)
	}
	if page.Tweets["post-2"].Text != "quoted context" || page.Media["media-1"].AltText != "A useful diagram" || page.NextToken != "next" {
		t.Fatalf("expanded context = %#v %#v token=%q", page.Tweets, page.Media, page.NextToken)
	}
}
