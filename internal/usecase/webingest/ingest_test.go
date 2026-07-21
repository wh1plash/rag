package webingest

import (
	"strings"
	"testing"
)

func TestSplitWebParts_ShortSnippetSingle(t *testing.T) {
	text := "Tavily is a search API for AI agents with clean snippets."
	parts := splitWebParts(text, 300, 50)
	if len(parts) != 1 {
		t.Fatalf("want 1 part, got %d: %#v", len(parts), parts)
	}
}

func TestSplitWebParts_ParagraphPacking(t *testing.T) {
	p1 := strings.Repeat("alpha ", 40) // 40 words
	p2 := strings.Repeat("beta ", 40)
	p3 := strings.Repeat("gamma ", 40)
	text := strings.TrimSpace(p1) + "\n\n" + strings.TrimSpace(p2) + "\n\n" + strings.TrimSpace(p3)
	parts := splitWebParts(text, 100, 20)
	if len(parts) < 2 {
		t.Fatalf("expected multiple parts, got %d", len(parts))
	}
	for i, p := range parts {
		n := len(strings.Fields(p))
		if n > 100 {
			t.Fatalf("part %d has %d words > 100", i, n)
		}
	}
}

func TestSplitByWords_Overlap(t *testing.T) {
	words := strings.Fields(strings.Repeat("word ", 250))
	parts := splitByWords(words, 100, 20)
	if len(parts) < 3 {
		t.Fatalf("got %d parts", len(parts))
	}
	if len(strings.Fields(parts[0])) != 100 {
		t.Fatalf("first part words=%d", len(strings.Fields(parts[0])))
	}
}

func TestCleanWebText_CollapsesBlankLines(t *testing.T) {
	in := "a\n\n\n\nb\n"
	got := cleanWebText(in)
	if got != "a\n\nb" {
		t.Fatalf("got %q", got)
	}
}

func TestDocIDFromURLStable(t *testing.T) {
	a := DocIDFromURL("https://example.com/a")
	b := DocIDFromURL("https://example.com/a")
	c := DocIDFromURL("https://example.com/b")
	if a != b {
		t.Fatal("expected stable id")
	}
	if a == c {
		t.Fatal("expected different urls to differ")
	}
}
