package main

import (
	"strings"
	"testing"
)

func TestPRRefShell(t *testing.T) {
	cur := prRefShell(0)
	if !strings.Contains(cur, "head=HEAD") || strings.Contains(cur, "pull/") || prRefCleanup(0) != "" {
		t.Fatalf("num=0 should use HEAD, no fetch of pull/, no cleanup: %q", cur)
	}
	pr := prRefShell(7)
	if !strings.Contains(pr, `pull/$NUM/head:refs/ci/pr`) || !strings.Contains(pr, "head=refs/ci/pr") || !strings.Contains(prRefCleanup(7), "update-ref -d refs/ci/pr") {
		t.Fatalf("num>0 should fetch pull head into refs/ci/pr and clean it up: %q", pr)
	}
}

func TestView(t *testing.T) {
	m := newModel("")
	m.width = 90
	m.loaded = true
	m.status = Status{Repo: true, Branch: "b", PR: &PR{
		Number: 1, State: "OPEN", Title: "t",
		Checks:       []Check{{Name: "a", Bucket: "pass"}, {Name: "b", Bucket: "fail"}},
		ChangedFiles: 1, Files: []File{{Path: "x", Additions: 1}},
	}}
	out := m.View()
	if !strings.Contains(out, "CI failing") {
		t.Fatal("missing headline")
	}
	if !strings.Contains(out, "pass") {
		t.Fatal("missing checks summary")
	}
}

func TestLogic(t *testing.T) {
	eq := func(got, want, msg string) {
		if got != want {
			t.Fatalf("%s: got %q want %q", msg, got, want)
		}
	}
	eq(ciSummary([]Check{{Bucket: "pass"}, {Bucket: "fail"}}).Overall, "fail", "fail wins")
	eq(ciSummary([]Check{{Bucket: "pass"}, {Bucket: "pending"}}).Overall, "pending", "pending over pass")
	eq(ciSummary([]Check{{Bucket: "pass"}}).Overall, "pass", "pass")
	eq(ciSummary(nil).Overall, "none", "none")
	eq(stateOf(Status{Repo: false}), "", "no repo")
	eq(stateOf(Status{Repo: true, PR: nil}), "", "no pr")
	eq(stateOf(Status{Repo: true, PR: &PR{State: "MERGED"}}), "merged", "merged")
	eq(stateOf(Status{Repo: true, PR: &PR{State: "OPEN", Checks: []Check{{Bucket: "fail"}}}}), "fail", "fail")
	eq(stateOf(Status{Repo: true, PR: &PR{State: "OPEN", Checks: []Check{{Bucket: "pending"}}}}), "run", "run")
	eq(trunc("hello", 3), "he…", "trunc")
	eq(truncTail("abcdef", 3), "…ef", "truncTail")
}
