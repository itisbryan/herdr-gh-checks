package main

// The side-effecting command layer: every tea.Cmd that shells out to gh/git/nvim/herdr.
// Kept separate from the TUI (model/Update/View in watch.go) so the render logic stays pure
// over state and the process-spawning surface lives in one place.

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

func fetchCmd(cwd string, num int) tea.Cmd {
	return func() tea.Msg { return fetchMsg(prStatusNum(cwd, num)) }
}
func workflowsCmd(cwd string) tea.Cmd {
	return func() tea.Msg { return workflowsMsg(listWorkflows(cwd)) }
}
func refetchCmd(cwd string, num int) tea.Cmd {
	return tea.Tick(5*time.Second, func(time.Time) tea.Msg { return fetchMsg(prStatusNum(cwd, num)) })
}

func runsCmd(cwd string) tea.Cmd   { return func() tea.Msg { return runsMsg(listRuns(cwd)) } }
func prListCmd(cwd string) tea.Cmd { return func() tea.Msg { return prListMsg(listPRs(cwd)) } }

// approvePR approves without a body (async).
func (m model) approvePR(number int) tea.Cmd {
	cwd := m.cwd
	return func() tea.Msg {
		c := exec.Command("gh", "pr", "review", strconv.Itoa(number), "--approve")
		c.Dir = cwd
		if out, err := c.CombinedOutput(); err != nil {
			return sentMsg(fmt.Sprintf("PR #%d approve failed: %s", number, strings.TrimSpace(string(out))))
		}
		return sentMsg(fmt.Sprintf("approved PR #%d", number))
	}
}

// prRefShell emits a shell snippet setting $base and $head for a PR review.
// num==0 = current branch (head=HEAD); num>0 fetches pull/$NUM/head into refs/ci/pr.
func prRefShell(num int) string {
	if num == 0 {
		return `base=$(gh pr view --json baseRefName -q .baseRefName 2>/dev/null); [ -z "$base" ] && base=main; git fetch -q origin "$base" 2>/dev/null; head=HEAD`
	}
	return `base=$(gh pr view "$NUM" --json baseRefName -q .baseRefName 2>/dev/null); [ -z "$base" ] && base=main; git fetch -q origin "$base" 2>/dev/null; git fetch -q origin "pull/$NUM/head:refs/ci/pr" 2>/dev/null; head=refs/ci/pr`
}

func prRefCleanup(num int) string {
	if num == 0 {
		return ""
	}
	return `; git update-ref -d refs/ci/pr 2>/dev/null`
}

// diffAll opens the whole PR side-by-side (base...head) in nvimdiff. num==0 = current branch.
func (m model) diffAll(num int) tea.Cmd {
	script := prRefShell(num) + `; git difftool -y -t nvimdiff "origin/$base...$head"` + prRefCleanup(num)
	c := exec.Command("sh", "-c", script)
	c.Dir = m.cwd
	c.Env = append(os.Environ(), "NUM="+strconv.Itoa(num))
	return tea.ExecProcess(c, func(error) tea.Msg { return reloadMsg{} })
}

// annotateFile reviews ONE file side-by-side with `ga` line annotation. num==0 diffs base vs the
// REAL working file (checked out) so review.vim reads the real path; num>0 diffs base vs a temp of
// the PR head and passes the path via CI_REVIEW_PATH.
func (m model) annotateFile(path string, num int) tea.Cmd {
	notes := notesFileFor(m.status.Branch)
	seedNotes(notes, m.status)
	env := append(os.Environ(), "CI_NOTES="+notes, "NUM="+strconv.Itoa(num), "FILE="+path, "VIMRC="+reviewVimPath())
	var body string
	if num == 0 {
		body = `a=$(mktemp); git show "origin/$base:$FILE" > "$a" 2>/dev/null; nvim -d "$a" "$FILE" -c 'wincmd l' -S "$VIMRC"; rm -f "$a"`
	} else {
		env = append(env, "CI_REVIEW_PATH="+path)
		body = `a=$(mktemp); b=$(mktemp); git show "origin/$base:$FILE" > "$a" 2>/dev/null; git show "$head:$FILE" > "$b" 2>/dev/null; nvim -d "$a" "$b" -c 'wincmd l' -S "$VIMRC"; rm -f "$a" "$b"`
	}
	script := prRefShell(num) + "; " + body + prRefCleanup(num)
	c := exec.Command("sh", "-c", script)
	c.Dir = m.cwd
	c.Env = env
	return tea.ExecProcess(c, func(error) tea.Msg { return reloadMsg{} })
}

// ghInteractive hands the terminal to gh's own review/comment flow (prompts + $EDITOR body).
func (m model) ghInteractive(kind string, number int) tea.Cmd {
	sub := "review"
	if kind == "comment" {
		sub = "comment"
	}
	c := exec.Command("gh", "pr", sub, strconv.Itoa(number))
	c.Dir = m.cwd
	return tea.ExecProcess(c, func(error) tea.Msg { return reloadMsg{} })
}

// openNotes edits the worktree's review file in nvim.
func (m model) openNotes() tea.Cmd {
	path := notesFileFor(m.status.Branch)
	seedNotes(path, m.status)
	c := exec.Command("nvim", path)
	c.Dir = m.cwd
	return tea.ExecProcess(c, func(error) tea.Msg { return reloadMsg{} })
}

// sendReview posts the annotations (minus # guide lines) to the chosen agent pane.
func (m model) sendReview(target string) tea.Cmd {
	branch, num := m.status.Branch, 0
	if m.status.PR != nil {
		num = m.status.PR.Number
	}
	return func() tea.Msg {
		body := readNotes(notesFileFor(branch))
		if body == "" {
			return sentMsg("no annotations — press a to write some")
		}
		text := fmt.Sprintf("Code review for PR #%d (%s):\n\n%s", num, branch, body)
		if err := exec.Command(herdrBin(), "agent", "prompt", target, text).Run(); err != nil {
			return sentMsg("send failed")
		}
		return sentMsg("sent to " + target)
	}
}

// updateBranch merges the latest base branch into the PR branch (GitHub's "Update branch").
func (m model) updateBranch() tea.Cmd {
	cwd := m.cwd
	return func() tea.Msg {
		c := exec.Command("gh", "pr", "update-branch")
		c.Dir = cwd
		if out, err := c.CombinedOutput(); err != nil {
			return sentMsg("update failed: " + strings.TrimSpace(string(out)))
		}
		return sentMsg("branch updated with base")
	}
}

// watchRun streams the workflow's latest run (gh run watch) until it completes.
func (m model) watchRun(id int) tea.Cmd {
	script := `rid=$(gh run list --workflow="$1" -L1 --json databaseId -q '.[0].databaseId' 2>/dev/null); if [ -n "$rid" ]; then gh run watch "$rid"; else echo "no runs yet — trigger one first (⏎)"; sleep 1; fi`
	c := exec.Command("sh", "-c", script, "sh", strconv.Itoa(id))
	c.Dir = m.cwd
	return tea.ExecProcess(c, func(error) tea.Msg { return reloadMsg{} })
}

func reviewVimPath() string {
	root := os.Getenv("HERDR_PLUGIN_ROOT")
	if root == "" {
		if self, err := os.Executable(); err == nil {
			root = filepath.Dir(self)
		}
	}
	return filepath.Join(root, "review.vim")
}
