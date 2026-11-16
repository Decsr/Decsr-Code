package main

import "testing"

func TestIssueWorkspaceOptions(t *testing.T) {
	tests := []struct {
		cmd     string
		args    []string
		want    string
		wantErr bool
	}{
		{"issue", []string{"42"}, "42", false},
		{"--issue", []string{"#42"}, "#42", false},
		{"--issue", []string{"https://github.com/o/r/issues/7", "--force"}, "https://github.com/o/r/issues/7 --force", false},
		{"--issue=42", nil, "42", false},
		{"--issue=42", []string{"--force"}, "42 --force", false},
		{"issue", []string{"--force", "bd-abc"}, "bd-abc --force", false},
		{"issue", nil, "", true},
		{"--issue", []string{"--bogus"}, "", true},
		{"issue", []string{"1", "2"}, "", true},
	}
	for _, tc := range tests {
		opts, err := issueWorkspaceOptions(tc.cmd, tc.args)
		if (err != nil) != tc.wantErr {
			t.Errorf("%s %v: err = %v, wantErr %v", tc.cmd, tc.args, err, tc.wantErr)
			continue
		}
		if opts.Issue != tc.want {
			t.Errorf("%s %v: Issue = %q, want %q", tc.cmd, tc.args, opts.Issue, tc.want)
		}
	}
}
