package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/Decsr/Decsr-Code/internal/artifacts"
)

func TestPrintReport_NoneExist(t *testing.T) {
	ws := artifacts.Workspace{Dir: t.TempDir()}
	var buf bytes.Buffer
	printReport(&buf, ws)
	out := buf.String()
	if !strings.Contains(out, "no reports yet") || !strings.Contains(out, "decsr task") {
		t.Errorf("expected helpful message, got %q", out)
	}
}

func TestPrintReport_OrderAndJSON(t *testing.T) {
	ws := artifacts.Workspace{Dir: t.TempDir()}
	for name, body := range map[string]string{
		artifacts.ReviewFile:     "rev",
		artifacts.SummaryFile:    "sum",
		artifacts.TestReportFile: `{"success":true}`,
	} {
		if err := ws.WriteFile(name, []byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	var buf bytes.Buffer
	printReport(&buf, ws)
	out := buf.String()
	if strings.Contains(out, "no reports yet") {
		t.Errorf("unexpected empty message: %q", out)
	}
	is, it, ir := strings.Index(out, "summary.md"), strings.Index(out, "test_report.json"), strings.Index(out, "review.md")
	if is < 0 || !(is < it && it < ir) {
		t.Errorf("unexpected order: %q", out)
	}
	if !strings.Contains(out, "  \"success\": true") {
		t.Errorf("json not pretty-printed: %q", out)
	}
}

func TestValidAgentRoles(t *testing.T) {
	got := strings.Join(validAgentRoles(), ", ")
	want := "coder, coder_fixer, pm, pr, qa, security, ux_reviewer"
	if got != want {
		t.Errorf("validAgentRoles = %q, want %q", got, want)
	}
}
