package agent

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/Decsr/Decsr-Code/internal/config"
)

func TestParseReport(t *testing.T) {
	cases := []struct {
		name, in string
		want     Report
		found    bool
		body     string
	}{
		{"done", "work\n===SUMMARY===\nsum\n\nREPORT\nstatus: done\nsummary: all good\n",
			Report{Status: "done", Summary: "all good"}, true, "work\n===SUMMARY===\nsum"},
		{"blocked", "x\nREPORT\nstatus: blocked\nsummary: no token\n",
			Report{Status: "blocked", Summary: "no token"}, true, "x"},
		{"question", "x\nREPORT\nstatus: question\nsummary: need a name\nquestion: Which name?\n",
			Report{Status: "question", Summary: "need a name", Question: "Which name?"}, true, "x"},
		{"unknown status is done", "REPORT\nstatus: maybe\nsummary: s\n", Report{Status: "done", Summary: "s"}, true, ""},
		{"echoed template is done", "REPORT\nstatus: done | blocked | question\nsummary: s\n", Report{Status: "done", Summary: "s"}, true, ""},
		{"fenced", "text\n```\nREPORT\nstatus: question\nsummary: s\nquestion: q?\n```\n",
			Report{Status: "question", Summary: "s", Question: "q?"}, true, "text"},
		{"bold", "text\n**REPORT**\n**status:** blocked\n**summary**: stuck\n",
			Report{Status: "blocked", Summary: "stuck"}, true, "text"},
		{"spaces and case", "text\n  report  \n  STATUS :   Question \n Summary:  s  \n QUESTION: q\n",
			Report{Status: "question", Summary: "s", Question: "q"}, true, "text"},
		{"crlf", "text\r\nREPORT\r\nstatus: question\r\nsummary: s\r\nquestion: q?\r\n",
			Report{Status: "question", Summary: "s", Question: "q?"}, true, "text"},
		{"multi-line question", "REPORT\nstatus: question\nsummary: s\nquestion: first line\nsecond line\n\nthird\n",
			Report{Status: "question", Summary: "s", Question: "first line\nsecond line\n\nthird"}, true, ""},
		{"multi-line summary then question", "REPORT\nstatus: question\nsummary: a\nb\nquestion: q\n",
			Report{Status: "question", Summary: "a\nb", Question: "q"}, true, ""},
		{"last report wins", "REPORT\nstatus: blocked\nsummary: old\nmore text\nREPORT\nstatus: done\nsummary: new\n",
			Report{Status: "done", Summary: "new"}, true, "REPORT\nstatus: blocked\nsummary: old\nmore text"},
		{"report word without keys is ignored", "REPORT\nthe report is below\n", Report{}, false, "REPORT\nthe report is below\n"},
		{"missing", "just text\n===SUMMARY===\nx", Report{}, false, "just text\n===SUMMARY===\nx"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, body, found := ParseReport(c.in)
			if found != c.found || !reflect.DeepEqual(got, c.want) {
				t.Fatalf("got %+v found=%v, want %+v found=%v", got, found, c.want, c.found)
			}
			if body != c.body {
				t.Errorf("body = %q, want %q", body, c.body)
			}
		})
	}
}

func TestReportAskAndWaits(t *testing.T) {
	if !(Report{Status: ReportQuestion}).Waits() || !(Report{Status: ReportBlocked}).Waits() || (Report{Status: ReportDone}).Waits() {
		t.Error("Waits")
	}
	if (Report{Question: " q ", Summary: "s"}).Ask() != "q" || (Report{Summary: "s"}).Ask() != "s" {
		t.Error("Ask")
	}
}

func TestWorkerPromptHasReportInstruction(t *testing.T) {
	for _, tc := range []struct {
		name     string
		mode     string
		readOnly bool
	}{
		{"text", config.ModeText, false}, {"text read-only", config.ModeText, true},
		{"agent", config.ModeAgent, false}, {"agent read-only", config.ModeAgent, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := &workerFake{chunks: []string{"found it\n===SUMMARY===\nok\n"}}
			_, err := RunWorker(context.Background(), f, t.TempDir(), config.DefaultConfig(),
				WorkerRequest{Title: "T", Mode: tc.mode, ReadOnly: tc.readOnly}, nil)
			if err != nil {
				t.Fatal(err)
			}
			for _, want := range []string{"REPORT\nstatus: done | blocked | question\nsummary:", "question: <only when status is question"} {
				if !strings.Contains(f.got.SystemPrompt, want) {
					t.Errorf("prompt lacks %q", want)
				}
			}
		})
	}
}

func TestRunWorkerReport(t *testing.T) {
	run := func(out string, ro bool) WorkerResult {
		f := &workerFake{chunks: []string{out}}
		res, err := RunWorker(context.Background(), f, t.TempDir(), config.DefaultConfig(),
			WorkerRequest{Title: "T", ReadOnly: ro}, nil)
		if err != nil {
			t.Fatal(err)
		}
		return res
	}
	res := run("analysis\n===SUMMARY===\nchecked it\n\nREPORT\nstatus: question\nsummary: s\nquestion: which db?\n", true)
	if res.Report.Status != ReportQuestion || res.Report.Question != "which db?" {
		t.Errorf("report = %+v", res.Report)
	}
	if res.Summary != "checked it" || strings.Contains(res.Summary, "REPORT") {
		t.Errorf("summary = %q", res.Summary)
	}
	res = run("did stuff\n===SUMMARY===\nplain summary\n", true)
	if res.Report.Status != ReportDone || res.Report.Summary != "plain summary" || res.Summary != "plain summary" {
		t.Errorf("missing report: %+v / %q", res.Report, res.Summary)
	}
	res = run("line one\nline two\n\nREPORT\nstatus: blocked\nsummary: stuck\n", true)
	if res.Summary != "line one\nline two" {
		t.Errorf("summary without marker = %q", res.Summary)
	}
	res = run("REPORT\nstatus: done\nsummary: only the report\n", true)
	if res.Summary != "only the report" {
		t.Errorf("summary = %q", res.Summary)
	}
}

func TestParseReportOptions(t *testing.T) {
	cases := []struct {
		name, in string
		opts     []string
		rec      int
	}{
		{"options and recommended",
			"REPORT\nstatus: question\nsummary: s\nquestion: Which port?\noptions:\n1. 8080 - matches the dev server\n2) 9090 - separate\n- 7070\nrecommended: 1\n",
			[]string{"8080 - matches the dev server", "9090 - separate", "7070"}, 1},
		{"recommended out of range",
			"REPORT\nstatus: question\nsummary: s\nquestion: q\noptions:\n1. a\n2. b\nrecommended: 5\n",
			[]string{"a", "b"}, 0},
		{"recommended without options",
			"REPORT\nstatus: question\nsummary: s\nquestion: q\nrecommended: 1\n", nil, 0},
		{"no options", "REPORT\nstatus: question\nsummary: s\nquestion: q\n", nil, 0},
		{"code fence",
			"x\n```\nREPORT\nstatus: question\nsummary: s\nquestion: q\noptions:\n1. a - x\n2. b - y\nrecommended: 2.\n```\n",
			[]string{"a - x", "b - y"}, 2},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, _, found := ParseReport(c.in)
			if !found || !reflect.DeepEqual(got.Options, c.opts) || got.Recommended != c.rec {
				t.Fatalf("got %+v, want options %q recommended %d", got, c.opts, c.rec)
			}
			if got.Question != "q" && got.Question != "Which port?" {
				t.Errorf("question = %q", got.Question)
			}
		})
	}
}

func TestRunWorkerBodyIsTheReplyWithoutTheReport(t *testing.T) {
	f := &workerFake{chunks: []string{"## Findings\n\n- one\n- two\n\nREPORT\nstatus: done\nsummary: two findings\n"}}
	res, err := RunWorker(context.Background(), f, t.TempDir(), config.DefaultConfig(),
		WorkerRequest{Title: "T", ReadOnly: true}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.Body != "## Findings\n\n- one\n- two" {
		t.Errorf("body = %q", res.Body)
	}
	f = &workerFake{chunks: []string{"just prose\n"}}
	res, err = RunWorker(context.Background(), f, t.TempDir(), config.DefaultConfig(),
		WorkerRequest{Title: "T", ReadOnly: true}, nil)
	if err != nil || res.Body != "just prose" {
		t.Errorf("no report: body %q err %v", res.Body, err)
	}
}
