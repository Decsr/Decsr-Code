package contextpack

import (
	"strings"
	"testing"
)

func TestEnsureUserBlock_AddsSection(t *testing.T) {
	doc := "# Repository Context Pack\n\nSome content."
	got := EnsureUserBlock(doc)

	if !strings.Contains(got, BeginMarker) || !strings.Contains(got, EndMarker) {
		t.Fatalf("expected user markers, got:\n%s", got)
	}
	if !strings.Contains(got, UserHeading) {
		t.Fatalf("expected user heading, got:\n%s", got)
	}
	if !strings.HasPrefix(got, doc) {
		t.Fatalf("expected original content to be preserved, got:\n%s", got)
	}
}

func TestEnsureUserBlock_PreservesExisting(t *testing.T) {
	doc := "# Pack\n\n" + UserHeading + "\n\n" + BeginMarker + "\nmy notes\n" + EndMarker + "\n"
	if got := EnsureUserBlock(doc); got != doc {
		t.Fatalf("expected existing block to be untouched, got:\n%s", got)
	}
}

func TestExtractAndReplaceUserBlock(t *testing.T) {
	doc := "# Pack\n\n" + UserHeading + "\n\n" + BeginMarker + "\nfirst notes\n" + EndMarker + "\n\n## 8. Open Questions\n"
	block, ok := ExtractUserBlock(doc)
	if !ok {
		t.Fatal("expected to extract user block")
	}
	if !strings.Contains(block, "first notes") {
		t.Fatalf("extracted block missing notes: %q", block)
	}

	replacement := BeginMarker + "\nsecond notes\n" + EndMarker
	got, replaced := ReplaceUserBlock(doc, replacement)
	if !replaced {
		t.Fatal("expected existing block to be replaced")
	}
	if strings.Contains(got, "first notes") {
		t.Fatalf("old notes should be removed:\n%s", got)
	}
	if !strings.Contains(got, "second notes") {
		t.Fatalf("new notes missing:\n%s", got)
	}
	if !strings.Contains(got, "## 8. Open Questions") {
		t.Fatalf("surrounding content should be preserved:\n%s", got)
	}
}

func TestReplaceUserBlock_AppendsWhenMissing(t *testing.T) {
	doc := "# Pack\n\nbody"
	block := BeginMarker + "\nnotes\n" + EndMarker
	got, replaced := ReplaceUserBlock(doc, block)

	if replaced {
		t.Fatal("expected replaced=false when no existing block")
	}
	if !strings.Contains(got, UserHeading) || !strings.Contains(got, "notes") {
		t.Fatalf("expected appended user section, got:\n%s", got)
	}
}

func TestReplaceUserBlock_RejectsInvalidBlock(t *testing.T) {
	doc := "# Pack"
	got, replaced := ReplaceUserBlock(doc, "not a block")
	if replaced || got != doc {
		t.Fatalf("invalid block should be rejected, got %q replaced=%v", got, replaced)
	}
}

func TestStripOuterCodeFence(t *testing.T) {
	doc := "```markdown\n# Pack\n\ncontent\n```"
	got := StripOuterCodeFence(doc)
	if got != "# Pack\n\ncontent" {
		t.Fatalf("unexpected stripped content: %q", got)
	}
}

func TestStripOuterCodeFence_LeavesInnerFence(t *testing.T) {
	doc := "# Pack\n\n```go\nfunc main() {}\n```\n"
	if got := StripOuterCodeFence(doc); got != doc {
		t.Fatalf("inner fence should not be stripped, got %q", got)
	}
}

func TestEnsureUserBlock_AddsHeadingWhenMarkersExist(t *testing.T) {
	doc := "# Pack\n\n" + BeginMarker + "\nnotes\n" + EndMarker
	got := EnsureUserBlock(doc)
	if !strings.Contains(got, UserHeading) {
		t.Fatalf("expected heading to be added:\n%s", got)
	}
	if !strings.Contains(got, "notes") {
		t.Fatalf("user notes should be preserved:\n%s", got)
	}
}
