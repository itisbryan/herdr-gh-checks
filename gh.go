package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

type Check struct {
	Name   string `json:"name"`
	State  string `json:"state"`
	Bucket string `json:"bucket"`
}
type File struct {
	Path      string `json:"path"`
	Additions int    `json:"additions"`
	Deletions int    `json:"deletions"`
}
type Label struct {
	Name  string `json:"name"`
	Color string `json:"color"`
}
type Assignee struct {
	Login string `json:"login"`
}
type PR struct {
	Number         int        `json:"number"`
	State          string     `json:"state"`
	Title          string     `json:"title"`
	Body           string     `json:"body"`
	URL            string     `json:"url"`
	Assignees      []Assignee `json:"assignees"`
	Labels         []Label    `json:"labels"`
	Files          []File     `json:"files"`
	Additions      int        `json:"additions"`
	Deletions      int        `json:"deletions"`
	ChangedFiles   int        `json:"changedFiles"`
	ReviewDecision string     `json:"reviewDecision"`
	HeadRefName    string     `json:"headRefName"`
	Checks         []Check    `json:"-"`
}
type Status struct {
	Repo   bool
	Branch string
	PR     *PR
}

// run executes name in cwd, returning trimmed stdout and ok=false on any error.
func run(cwd, name string, args ...string) (string, bool) {
	cmd := exec.Command(name, args...)
	if cwd != "" {
		cmd.Dir = cwd
	}
	out, err := cmd.Output()
	if err != nil {
		return "", false
	}
	return strings.TrimSpace(string(out)), true
}

func prStatus(cwd string) Status { return prStatusNum(cwd, 0) }

// prStatusNum fetches the current branch's PR (num=0) or a specific PR by number.
func prStatusNum(cwd string, num int) Status {
	branch, ok := run(cwd, "git", "rev-parse", "--abbrev-ref", "HEAD")
	if !ok {
		return Status{Repo: false}
	}
	viewArgs := []string{"pr", "view"}
	checkArgs := []string{"pr", "checks"}
	if num > 0 {
		viewArgs = append(viewArgs, strconv.Itoa(num))
		checkArgs = append(checkArgs, strconv.Itoa(num))
	}
	viewArgs = append(viewArgs, "--json",
		"number,state,title,body,url,assignees,labels,files,additions,deletions,changedFiles,reviewDecision,headRefName")
	viewRaw, ok := run(cwd, "gh", viewArgs...)
	if !ok {
		return Status{Repo: true, Branch: branch}
	}
	var pr PR
	if json.Unmarshal([]byte(viewRaw), &pr) != nil {
		return Status{Repo: true, Branch: branch}
	}
	if pr.State == "OPEN" {
		checkArgs = append(checkArgs, "--json", "name,state,bucket")
		if raw, ok := run(cwd, "gh", checkArgs...); ok {
			json.Unmarshal([]byte(raw), &pr.Checks)
		}
	}
	if num > 0 && pr.HeadRefName != "" {
		branch = pr.HeadRefName
	}
	return Status{Repo: true, Branch: branch, PR: &pr}
}

type Summary struct {
	Pass, Fail, Pending int
	Overall             string
}

func ciSummary(checks []Check) Summary {
	s := Summary{}
	for _, c := range checks {
		switch c.Bucket {
		case "pass":
			s.Pass++
		case "fail":
			s.Fail++
		case "pending":
			s.Pending++
		}
	}
	switch {
	case len(checks) == 0:
		s.Overall = "none"
	case s.Fail > 0:
		s.Overall = "fail"
	case s.Pending > 0:
		s.Overall = "pending"
	default:
		s.Overall = "pass"
	}
	return s
}

// sidebar state: pass | fail | run | merged | open | "" (no PR)
func stateOf(st Status) string {
	if !st.Repo || st.PR == nil {
		return ""
	}
	switch st.PR.State {
	case "MERGED":
		return "merged"
	case "CLOSED":
		return "fail"
	}
	switch ciSummary(st.PR.Checks).Overall {
	case "fail":
		return "fail"
	case "pending":
		return "run"
	case "pass":
		return "pass"
	default:
		return "open"
	}
}

func settled(s Status) bool {
	if !s.Repo {
		return true
	}
	if s.PR == nil {
		return false
	}
	return s.PR.State == "MERGED" || s.PR.State == "CLOSED"
}

func env(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

// ---------- review: agents + notes ----------
type Agent struct {
	Kind        string `json:"agent"`
	Name        string `json:"name"`
	PaneID      string `json:"pane_id"`
	WorkspaceID string `json:"workspace_id"`
	Status      string `json:"agent_status"`
}

func listAgents() []Agent {
	out, ok := run("", herdrBin(), "agent", "list")
	if !ok {
		return nil
	}
	var m struct {
		Result struct {
			Agents []Agent `json:"agents"`
		} `json:"result"`
	}
	if json.Unmarshal([]byte(out), &m) != nil {
		return nil
	}
	return m.Result.Agents
}

func stateDir() string {
	if d := os.Getenv("HERDR_PLUGIN_STATE_DIR"); d != "" {
		return d
	}
	return os.TempDir()
}

// one persistent review file per worktree branch
func notesFileFor(branch string) string {
	slug := strings.NewReplacer("/", "-", " ", "-").Replace(branch)
	if slug == "" {
		slug = "review"
	}
	return filepath.Join(stateDir(), "review-"+slug+".md")
}

func seedNotes(path string, s Status) {
	if _, err := os.Stat(path); err == nil {
		return
	}
	var b strings.Builder
	num := 0
	if s.PR != nil {
		num = s.PR.Number
	}
	fmt.Fprintf(&b, "# Review — #%d %s\n", num, s.Branch)
	b.WriteString("# Annotate below, e.g.:  app/models/x.rb:42  handle nil here\n# (lines starting with # are not sent)\n#\n# Changed files:\n")
	if s.PR != nil {
		for _, f := range s.PR.Files {
			fmt.Fprintf(&b, "#   %s\n", f.Path)
		}
	}
	b.WriteString("\n")
	_ = os.WriteFile(path, []byte(b.String()), 0o644)
}

// loadNotes returns the annotation lines (no # guide/blank lines).
func loadNotes(branch string) []string {
	var out []string
	for _, ln := range strings.Split(readNotes(notesFileFor(branch)), "\n") {
		if strings.TrimSpace(ln) != "" {
			out = append(out, ln)
		}
	}
	return out
}

func writeNotes(branch string, lines []string) {
	_ = os.WriteFile(notesFileFor(branch), []byte(strings.Join(lines, "\n")+"\n"), 0o644)
}

func readNotes(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	var out []string
	for _, ln := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(strings.TrimSpace(ln), "#") {
			continue
		}
		out = append(out, ln)
	}
	return strings.TrimSpace(strings.Join(out, "\n"))
}

// ---------- GitHub Actions workflows ----------
type Workflow struct {
	ID    int    `json:"id"`
	Name  string `json:"name"`
	State string `json:"state"`
}

func listWorkflows(cwd string) []Workflow {
	out, ok := run(cwd, "gh", "workflow", "list", "--json", "name,id,state")
	if !ok {
		return nil
	}
	var w []Workflow
	_ = json.Unmarshal([]byte(out), &w)
	return w
}

// ponytail: gh errors if the workflow lacks a workflow_dispatch trigger; we just surface that.
func runWorkflow(cwd string, id int, ref string) error {
	c := exec.Command("gh", "workflow", "run", strconv.Itoa(id), "--ref", ref)
	c.Dir = cwd
	return c.Run()
}

// active = most recently committed; capped so long-lived repos don't dump every stale branch.
type Run struct {
	WorkflowID int    `json:"workflowDatabaseId"`
	Status     string `json:"status"`     // queued, in_progress, completed
	Conclusion string `json:"conclusion"` // success, failure, ...
}

// latest run per workflow (gh run list is newest-first)
func listRuns(cwd string) map[int]Run {
	out, ok := run(cwd, "gh", "run", "list", "-L", "30", "--json", "workflowDatabaseId,status,conclusion")
	if !ok {
		return nil
	}
	var rs []Run
	if json.Unmarshal([]byte(out), &rs) != nil {
		return nil
	}
	m := map[int]Run{}
	for _, r := range rs {
		if _, seen := m[r.WorkflowID]; !seen {
			m[r.WorkflowID] = r
		}
	}
	return m
}

type PRItem struct {
	Number         int    `json:"number"`
	Title          string `json:"title"`
	HeadRefName    string `json:"headRefName"`
	ReviewDecision string `json:"reviewDecision"`
	IsDraft        bool   `json:"isDraft"`
	Author         struct {
		Login string `json:"login"`
	} `json:"author"`
}

func listPRs(cwd string) []PRItem {
	out, ok := run(cwd, "gh", "pr", "list", "--limit", "30", "--json", "number,title,headRefName,reviewDecision,isDraft,author")
	if !ok {
		return nil
	}
	var ps []PRItem
	_ = json.Unmarshal([]byte(out), &ps)
	return ps
}

func listBranches(cwd string) []string {
	out, ok := run(cwd, "git", "for-each-ref", "--sort=-committerdate", "--format=%(refname:short)", "refs/remotes/origin")
	if !ok {
		return nil
	}
	var b []string
	for _, ln := range strings.Split(out, "\n") {
		ln = strings.TrimPrefix(strings.TrimSpace(ln), "origin/")
		if ln == "" || ln == "HEAD" {
			continue
		}
		b = append(b, ln)
		if len(b) >= 30 {
			break
		}
	}
	return b
}

func herdrBin() string { return env("HERDR_BIN_PATH", "herdr") }
func pluginID() string { return env("HERDR_PLUGIN_ID", "herder-gh-checks") }
func ciCwd() string {
	if c := os.Getenv("CI_CWD"); c != "" {
		return c
	}
	wd, _ := os.Getwd()
	return wd
}
