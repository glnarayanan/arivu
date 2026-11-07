package bookmarks

import "testing"

func TestParseFeedAtomPrefersAlternateLink(t *testing.T) {
	raw := `<?xml version="1.0" encoding="utf-8"?>
<feed xmlns="http://www.w3.org/2005/Atom">
  <entry>
    <title>Rel alternate</title>
    <id>urn:1</id>
    <link rel="replies" href="https://example.com/a/comments"/>
    <link rel="alternate" href="https://example.com/a"/>
  </entry>
  <entry>
    <title>No rel</title>
    <id>urn:2</id>
    <link rel="enclosure" href="https://example.com/b.mp3"/>
    <link href="https://example.com/b"/>
  </entry>
  <entry>
    <title>Only other rels</title>
    <id>urn:3</id>
    <link rel="via" href="https://example.com/c-source"/>
    <link rel="replies" href="https://example.com/c/comments"/>
  </entry>
</feed>`
	items, err := parseFeed([]byte(raw))
	if err != nil {
		t.Fatalf("parseFeed: %v", err)
	}
	want := []string{"https://example.com/a", "https://example.com/b", "https://example.com/c-source"}
	if len(items) != len(want) {
		t.Fatalf("got %d items, want %d", len(items), len(want))
	}
	for i, w := range want {
		if items[i].Link != w {
			t.Errorf("item %d link = %q, want %q", i, items[i].Link, w)
		}
	}
}
