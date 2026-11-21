package workspace

import (
	"reflect"
	"testing"
)

func TestParseSplitReadsTheAskBlock(t *testing.T) {
	out := "Two ways to store it.\n===ASK===\nWhich database should the service use?\n" +
		"- SQLite - no server to run (recommended)\n- PostgreSQL - better for many writers\n===END===\n"
	g, err := ParseSplit("t", out, nil)
	if err != nil {
		t.Fatal(err)
	}
	want := &LeadAsk{Question: "Which database should the service use?",
		Options: []string{"SQLite - no server to run", "PostgreSQL - better for many writers"}, Recommended: 1}
	if !reflect.DeepEqual(g.Ask, want) {
		t.Errorf("ask = %+v, want %+v", g.Ask, want)
	}
	if g.Answer != "Two ways to store it." || len(g.Nodes) != 0 {
		t.Errorf("answer = %q, nodes = %d", g.Answer, len(g.Nodes))
	}
}

func TestParseSplitAskWinsOverAPlan(t *testing.T) {
	out := "===ASK===\nPick one?\n- A\n- B (Recommended)\n===TASKS===\n[{\"key\":\"T1\",\"title\":\"x\",\"description\":\"y\"}]\n===END===\n"
	g, err := ParseSplit("t", out, nil)
	if err != nil {
		t.Fatal(err)
	}
	if g.Ask == nil || g.Ask.Recommended != 2 || len(g.Nodes) != 0 {
		t.Errorf("ask = %+v, nodes = %d", g.Ask, len(g.Nodes))
	}
}

func TestParseAskLimits(t *testing.T) {
	if a := parseAsk("===ASK===\nQ?\n- only one\n===END==="); a != nil {
		t.Errorf("one option is not a choice: %+v", a)
	}
	if a := parseAsk("===ASK===\n- A\n- B\n===END==="); a != nil {
		t.Errorf("no question: %+v", a)
	}
	a := parseAsk("===ASK===\nQ?\n- A\n- B\n- C\n- D\n- E\n===END===")
	if a == nil || len(a.Options) != 4 || a.Recommended != 0 {
		t.Errorf("at most four options, none recommended: %+v", a)
	}
}
