package decsr

import "testing"

func TestCollectStats_CountsCoderAndQAFiles(t *testing.T) {
	tr := &TaskRunner{touched: make(map[string]bool)}
	tr.markTouched([]string{"main.go"})
	tr.markTouched([]string{"main_test.go"})
	tr.markTouched([]string{"main.go", "", "main_test.go"})

	if got := tr.collectStats().filesTouched; got != 2 {
		t.Errorf("filesTouched = %d, want 2", got)
	}
}
