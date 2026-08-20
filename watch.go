package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/charmbracelet/bubbles/progress"
	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

func trunc(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}
func truncTail(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return "…" + string(r[len(r)-(n-1):])
}

type fetchMsg Status
type workflowsMsg []Workflow
type reloadMsg struct{} // after an external process exits, refetch ASYNC (prStatus is ~2.4s; never in an ExecProcess callback on the main loop)
type runsMsg map[int]Run
type prListMsg []PRItem

type model struct {
	cwd       string
	self      string // path to this binary, for the merge shell-out
	status    Status
	loaded    bool
	cursor    int  // selected file in the FILES list
	filtering bool // typing in the file filter
	picking   bool // agent-picker modal open
	agents    []Agent
	pick      int
	focus     int // 0 = PR/files list, 1 = Workflows list
	workflows []Workflow
	runs      map[int]Run // latest run status per workflow
	wfpick    int
	wfBranch  bool // branch-pick step of the workflow trigger
	branches  []string
	bpick     int
	sent      string // last send status
	updating  bool   // update-branch in flight
	notesMode bool   // notes manager modal open
	notes     []string
	ncur      int
	recap     string  // the annotated code line, for the selected note
	folds     [4]bool // collapse Description / Checks / Files / Workflows
	wfLoaded  bool    // workflows fetched once
	width     int
	sp        spinner.Model // bubbles/spinner drives all spinner frames
	prog      progress.Model
	ti        textinput.Model // file filter input
	prPick    bool            // PR picker overlay (review other PRs)
	prList    []PRItem
	ppick     int
	viewNum   int // >0 = viewing another PR (read-only rich view) instead of the branch's PR
}

func newModel(cwd string) model {
	self, _ := os.Executable()
	sp := spinner.New(spinner.WithSpinner(spinner.Dot))
	sp.Style = lipgloss.NewStyle()                   // no color; callers wrap the frame themselves
	pr := progress.New(progress.WithoutPercentage()) // solid fill; color set per CI state in View
	pr.Width = 34
	ti := textinput.New()
	ti.Prompt = ""
	ti.Placeholder = "filter"
	ti.CharLimit = 80
	ti.Width = 24
	return model{cwd: cwd, self: self, sp: sp, prog: pr, ti: ti}
}

func prDecision(d string) string {
	switch d {
	case "APPROVED":
		return green.Render("✓")
	case "CHANGES_REQUESTED":
		return red.Render("✗")
	case "REVIEW_REQUIRED":
		return yellow.Render("●")
	default:
		return dim.Render("·")
	}
}

func (m model) prPickerView() string {
	L := []string{"", "  " + sect.Render("REVIEW A PULL REQUEST"), ""}
	w := m.width - 6
	if w < 20 {
		w = 70
	}
	const win = 15
	start := 0
	if m.ppick >= win {
		start = m.ppick - win + 1
	}
	end := start + win
	if end > len(m.prList) {
		end = len(m.prList)
	}
	for i := start; i < end; i++ {
		pr := m.prList[i]
		label := dim.Render(fmt.Sprintf("#%d", pr.Number)) + " " + prDecision(pr.ReviewDecision) + " " + trunc(pr.Title, w-26) + "  " + dim.Render("@"+pr.Author.Login)
		if pr.IsDraft {
			label = dim.Render("draft ") + label
		}
		if i == m.ppick {
			L = append(L, "  "+cyan.Render("› ")+label)
		} else {
			L = append(L, "    "+label)
		}
	}
	L = append(L, "", "  "+dim.Render("↑↓ pick · ⏎ view diff · a approve · r review · c comment · o web · esc back"))
	return strings.Join(L, "\n")
}

func (m model) Init() tea.Cmd { return tea.Batch(fetchCmd(m.cwd, 0), runsCmd(m.cwd), m.sp.Tick) }

// runBadge is the workflow's latest-run status inline in the section (spinner while running).
func (m model) runBadge(id int, frame string) string {
	r, ok := m.runs[id]
	if !ok {
		return ""
	}
	if r.Status == "completed" {
		if r.Conclusion == "success" {
			return "  " + green.Render("✓")
		}
		return "  " + red.Render("✗ "+r.Conclusion)
	}
	return "  " + yellow.Render(frame+" "+r.Status)
}

type sentMsg string

func (m model) filteredFiles() []File {
	if m.status.PR == nil {
		return nil
	}
	q := strings.ToLower(strings.TrimSpace(m.ti.Value()))
	if q == "" {
		return m.status.PR.Files
	}
	var out []File
	for _, f := range m.status.PR.Files {
		if strings.Contains(strings.ToLower(f.Path), q) {
			out = append(out, f)
		}
	}
	return out
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		w := msg.Width - 40 // leave room for the headline text on the same line
		if w > 28 {
			w = 28
		}
		if w < 12 {
			w = 12
		}
		m.prog.Width = w
	case sentMsg:
		wasUpdating := m.updating
		m.sent = string(msg)
		m.updating = false
		if wasUpdating { // after update-branch, refresh state (behind→clean, new checks)
			return m, fetchCmd(m.cwd, m.viewNum)
		}
		return m, nil
	case tea.KeyMsg:
		if m.notesMode { // notes manager: navigate, x delete, e edit
			switch msg.String() {
			case "esc", "q":
				m.notesMode = false
			case "up", "k":
				if m.ncur > 0 {
					m.ncur--
				}
			case "down", "j":
				if m.ncur < len(m.notes)-1 {
					m.ncur++
				}
			case "x", "d":
				if m.ncur < len(m.notes) {
					m.notes = append(m.notes[:m.ncur], m.notes[m.ncur+1:]...)
					writeNotes(m.status.Branch, m.notes)
					if m.ncur >= len(m.notes) && m.ncur > 0 {
						m.ncur--
					}
				}
			case "e", "enter":
				m.notesMode = false
				return m, m.openNotes()
			}
			m.recap = m.recapFor()
			return m, nil
		}
		if m.wfBranch { // choose the branch to run the workflow on
			switch msg.String() {
			case "esc", "q":
				m.wfBranch = false
			case "up", "k":
				if m.bpick > 0 {
					m.bpick--
				}
			case "down", "j":
				if m.bpick < len(m.branches)-1 {
					m.bpick++
				}
			case "enter":
				if m.wfpick < len(m.workflows) && m.bpick < len(m.branches) {
					wf, ref := m.workflows[m.wfpick], m.branches[m.bpick]
					m.wfBranch = false
					return m, func() tea.Msg {
						if err := runWorkflow(m.cwd, wf.ID, ref); err != nil {
							return sentMsg("trigger failed: " + wf.Name)
						}
						return sentMsg("triggered " + wf.Name + " on " + ref)
					}
				}
			}
			return m, nil
		}
		if m.prPick { // browse other PRs to review / approve / comment
			switch msg.String() {
			case "esc", "q":
				m.prPick = false
			case "up", "k":
				if m.ppick > 0 {
					m.ppick--
				}
			case "down", "j":
				if m.ppick < len(m.prList)-1 {
					m.ppick++
				}
			case "o":
				if m.ppick < len(m.prList) {
					c := exec.Command("gh", "pr", "view", strconv.Itoa(m.prList[m.ppick].Number), "--web")
					c.Dir = m.cwd
					_ = c.Start()
				}
			case "a": // quick approve, no body
				if m.ppick < len(m.prList) {
					n := m.prList[m.ppick].Number
					m.prPick = false
					return m, m.approvePR(n)
				}
			case "enter", "v": // open the PR in the full rich pane (looks like your current PR)
				if m.ppick < len(m.prList) {
					m.viewNum = m.prList[m.ppick].Number
					m.prPick = false
					m.loaded = false
					m.cursor = 0
					return m, fetchCmd(m.cwd, m.viewNum)
				}
			case "r": // submit a review: approve / comment / request-changes + body
				if m.ppick < len(m.prList) {
					n := m.prList[m.ppick].Number
					m.prPick = false
					return m, m.ghInteractive("review", n)
				}
			case "c": // plain comment (editor)
				if m.ppick < len(m.prList) {
					n := m.prList[m.ppick].Number
					m.prPick = false
					return m, m.ghInteractive("comment", n)
				}
			}
			return m, nil
		}
		if m.viewNum > 0 { // viewing another PR: rich read view + review actions (current-PR edit keys disabled)
			switch msg.String() {
			case "esc", "q":
				m.viewNum = 0
				m.loaded = false
				m.cursor = 0
				return m, fetchCmd(m.cwd, 0)
			case "ctrl+c":
				return m, tea.Quit
			case "p":
				return m, prListCmd(m.cwd)
			case "a":
				return m, m.approvePR(m.viewNum)
			case "r":
				return m, m.ghInteractive("review", m.viewNum)
			case "c":
				return m, m.ghInteractive("comment", m.viewNum)
			case "d":
				return m, m.diffAll(m.viewNum)
			case "enter":
				if ff := m.filteredFiles(); m.cursor < len(ff) {
					return m, m.annotateFile(ff[m.cursor].Path, m.viewNum)
				}
			case "o":
				c := exec.Command("gh", "pr", "view", strconv.Itoa(m.viewNum), "--web")
				c.Dir = m.cwd
				_ = c.Start()
			case "up", "k":
				if m.cursor > 0 {
					m.cursor--
				}
			case "down", "j":
				if m.cursor < len(m.filteredFiles())-1 {
					m.cursor++
				}
			}
			return m, nil
		}
		if m.picking { // agent-picker modal
			switch msg.String() {
			case "esc", "q":
				m.picking = false
			case "up", "k":
				if m.pick > 0 {
					m.pick--
				}
			case "down", "j":
				if m.pick < len(m.agents)-1 {
					m.pick++
				}
			case "enter":
				if m.pick < len(m.agents) {
					m.picking = false
					return m, m.sendReview(m.agents[m.pick].PaneID)
				}
			}
			return m, nil
		}
		ff := m.filteredFiles()
		if m.filtering { // type to narrow the file list; arrows navigate, enter opens, esc cancels
			switch msg.String() {
			case "esc":
				m.filtering = false
				m.ti.Blur()
				m.ti.SetValue("")
				m.cursor = 0
				return m, nil
			case "enter":
				if m.cursor < len(ff) {
					return m, m.annotateFile(ff[m.cursor].Path, 0)
				}
				return m, nil
			case "up":
				if m.cursor > 0 {
					m.cursor--
				}
				return m, nil
			case "down":
				if m.cursor < len(ff)-1 {
					m.cursor++
				}
				return m, nil
			default:
				var cmd tea.Cmd
				m.ti, cmd = m.ti.Update(msg)
				if m.cursor >= len(m.filteredFiles()) {
					m.cursor = 0
				}
				return m, cmd
			}
		}
		switch msg.String() {
		case "q", "ctrl+c", "esc":
			return m, tea.Quit
		case "/":
			if m.status.PR != nil && len(m.status.PR.Files) > 0 {
				m.filtering = true
				m.cursor = 0
				return m, m.ti.Focus()
			}
		case "m": // suspend the TUI, run the interactive merge, resume + refetch
			if m.self != "" && m.status.PR != nil && m.status.PR.State == "OPEN" {
				c := exec.Command(m.self, "--merge")
				c.Env = append(os.Environ(), "CI_CWD="+m.cwd)
				return m, tea.ExecProcess(c, func(error) tea.Msg { return reloadMsg{} })
			}
		case "o": // open the PR on the web
			if m.status.PR != nil {
				c := exec.Command("gh", "pr", "view", "--web")
				c.Dir = m.cwd
				_ = c.Start()
			}
		case "d": // review ALL PR files in nvim, side-by-side; :qa advances
			if m.status.PR != nil {
				return m, m.diffAll(0)
			}
		case "u": // update branch with latest base branch
			if m.status.PR != nil && m.status.PR.State == "OPEN" {
				m.updating = true
				m.sent = ""
				return m, m.updateBranch()
			}
		case "1":
			m.folds[0] = !m.folds[0]
		case "2":
			m.folds[1] = !m.folds[1]
		case "3":
			m.folds[2] = !m.folds[2]
		case "4":
			m.folds[3] = !m.folds[3]
		case "a": // notes manager: review / edit / delete annotations
			if m.status.PR != nil {
				m.notes = loadNotes(m.status.Branch)
				m.ncur = 0
				m.notesMode = true
				m.recap = m.recapFor()
			}
		case "s": // send review: pick an agent in THIS space
			if m.status.PR != nil {
				ws := os.Getenv("HERDR_WORKSPACE_ID")
				m.agents = nil
				for _, a := range listAgents() {
					if ws == "" || a.WorkspaceID == ws {
						m.agents = append(m.agents, a)
					}
				}
				if len(m.agents) > 0 {
					m.picking = true
					m.pick = 0
				}
			}
		case "p": // browse open PRs to review / approve / comment (any PR, not just this branch)
			return m, prListCmd(m.cwd)
		case "tab": // switch focus between the PR files and the Workflows list (reveal the target)
			if len(m.workflows) > 0 {
				m.focus = 1 - m.focus
				if m.focus == 0 {
					m.folds[2] = false
				} else {
					m.folds[3] = false
				}
			}
		case "w": // jump focus to Workflows (reveal it)
			if len(m.workflows) > 0 {
				m.focus = 1
				m.folds[3] = false
			}
		case "v": // watch the selected workflow's latest run
			if m.focus == 1 && m.wfpick < len(m.workflows) {
				return m, m.watchRun(m.workflows[m.wfpick].ID)
			}
		case "up", "k":
			if m.focus == 1 {
				if m.wfpick > 0 {
					m.wfpick--
				}
			} else if m.cursor > 0 {
				m.cursor--
			}
		case "down", "j":
			if m.focus == 1 {
				if m.wfpick < len(m.workflows)-1 {
					m.wfpick++
				}
			} else if m.cursor < len(ff)-1 {
				m.cursor++
			}
		case "enter":
			if m.focus == 1 { // trigger selected workflow → choose branch
				if m.wfpick < len(m.workflows) {
					m.branches = listBranches(m.cwd)
					if len(m.branches) == 0 {
						m.branches = []string{m.status.Branch}
					}
					m.bpick = 0
					for i, b := range m.branches {
						if b == m.status.Branch {
							m.bpick = i
							break
						}
					}
					m.wfBranch = true
				}
			} else if m.cursor < len(ff) { // review the selected file's diff (ga to annotate)
				return m, m.annotateFile(ff[m.cursor].Path, 0)
			}
		}
	case reloadMsg:
		return m, fetchCmd(m.cwd, m.viewNum) // async; keeps the pane responsive after nvim/merge exits
	case prListMsg:
		m.prList = msg
		if len(m.prList) > 0 {
			m.prPick = true
			m.ppick = 0
		}
		return m, nil
	case runsMsg:
		m.runs = msg
		return m, nil
	case workflowsMsg:
		m.workflows = msg
		if len(m.workflows) == 0 {
			m.focus = 0
		}
		return m, nil
	case fetchMsg:
		m.status = Status(msg)
		m.loaded = true
		if m.cursor >= len(m.filteredFiles()) {
			m.cursor = 0
		}
		var cmds []tea.Cmd
		if m.status.Repo && !m.wfLoaded { // fetch workflows once (independent of PR — you can run them on any branch)
			m.wfLoaded = true
			cmds = append(cmds, workflowsCmd(m.cwd))
		}
		if m.status.PR != nil && len(m.status.PR.Checks) > 0 {
			sm := ciSummary(m.status.PR.Checks)
			cmds = append(cmds, m.prog.SetPercent(float64(sm.Pass)/float64(len(m.status.PR.Checks)))) // animated fill
		}
		if !settled(m.status) {
			cmds = append(cmds, refetchCmd(m.cwd, m.viewNum))
			// gh run list is ~2.4s; only refresh run badges when you're actually watching the
			// workflows section on your own PR (not while reviewing another PR or with it folded).
			if m.viewNum == 0 && len(m.workflows) > 0 && !m.folds[3] {
				cmds = append(cmds, runsCmd(m.cwd))
			}
		}
		return m, tea.Batch(cmds...)
	case spinner.TickMsg:
		var cmd tea.Cmd
		m.sp, cmd = m.sp.Update(msg)
		return m, cmd
	case progress.FrameMsg:
		pm, cmd := m.prog.Update(msg)
		m.prog = pm.(progress.Model)
		return m, cmd
	}
	return m, nil
}

func (m model) pickerView() string {
	w := m.width - 4
	if w < 20 {
		w = 76
	}
	L := []string{"", "  " + sect.Render("REVIEW TO SEND"), ""}
	notes := readNotes(notesFileFor(m.status.Branch))
	if strings.TrimSpace(notes) == "" {
		L = append(L, "  "+dim.Render("(no annotations — esc, then ⏎/ga to add)"))
	} else {
		lines := strings.Split(notes, "\n")
		for i, ln := range lines {
			if i >= 12 {
				L = append(L, "  "+dim.Render(fmt.Sprintf("… +%d more", len(lines)-12)))
				break
			}
			ln = trunc(ln, w)
			if idx := strings.Index(ln, "  "); idx > 0 { // color the path:line prefix
				L = append(L, "  "+cyan.Render(ln[:idx])+ln[idx:])
			} else {
				L = append(L, "  "+ln)
			}
		}
	}
	L = append(L, "", "  "+sect.Render("SEND TO AGENT"), "")
	for i, a := range m.agents {
		st := green // idle
		switch a.Status {
		case "working":
			st = yellow
		case "blocked":
			st = red
		case "unknown", "":
			st = dim
		}
		label := bold.Render(a.Kind) + "  " + st.Render("● "+a.Status) + "  " + dim.Render(a.PaneID)
		if i == m.pick {
			L = append(L, "  "+cyan.Render("› ")+label)
		} else {
			L = append(L, "    "+label)
		}
	}
	L = append(L, "", "  "+dim.Render("↑↓ pick · ⏎ send · esc cancel"))
	return strings.Join(L, "\n")
}

// codeLineAt reads the annotated source line (note = "path:line  ...").
func codeLineAt(cwd, note string) string {
	loc := note
	if i := strings.Index(note, "  "); i > 0 {
		loc = note[:i]
	}
	c := strings.LastIndex(loc, ":")
	if c < 0 {
		return ""
	}
	n, err := strconv.Atoi(loc[c+1:])
	if err != nil || n < 1 {
		return ""
	}
	data, err := os.ReadFile(filepath.Join(cwd, loc[:c]))
	if err != nil {
		return ""
	}
	lines := strings.Split(string(data), "\n")
	if n > len(lines) {
		return ""
	}
	return strings.TrimSpace(lines[n-1])
}

func fhdr(label string, folded bool, key string) string {
	arrow := "▾ "
	if folded {
		arrow = "▸ "
	}
	return dim.Render(arrow) + sect.Render(label) + dim.Render("  "+key)
}

func (m model) recapFor() string {
	if m.ncur < len(m.notes) {
		return codeLineAt(m.cwd, m.notes[m.ncur])
	}
	return ""
}

func (m model) notesView() string {
	L := []string{"", "  " + sect.Render("REVIEW NOTES"), ""}
	if len(m.notes) == 0 {
		L = append(L, "  "+dim.Render("(empty — press e to add)"))
	} else {
		w := m.width - 4
		if w < 20 {
			w = 76
		}
		for i, ln := range m.notes {
			row := trunc(ln, w)
			if idx := strings.Index(row, "  "); idx > 0 {
				row = cyan.Render(row[:idx]) + row[idx:]
			}
			if i == m.ncur {
				L = append(L, "  "+cyan.Render("› ")+row)
				if m.recap != "" {
					L = append(L, "      "+dim.Render("┆ "+trunc(m.recap, w-8)))
				}
			} else {
				L = append(L, "    "+row)
			}
		}
	}
	L = append(L, "", "  "+dim.Render("↑↓ move · x delete · e edit · esc back"))
	return strings.Join(L, "\n")
}

func (m model) wfBranchView() string {
	wf := ""
	if m.wfpick < len(m.workflows) {
		wf = m.workflows[m.wfpick].Name
	}
	L := []string{"", "  " + sect.Render("RUN ON BRANCH"), "  " + dim.Render(wf), ""}
	const win = 15
	start := 0
	if m.bpick >= win {
		start = m.bpick - win + 1
	}
	end := start + win
	if end > len(m.branches) {
		end = len(m.branches)
	}
	for i := start; i < end; i++ {
		b := m.branches[i]
		mark := ""
		if b == m.status.Branch {
			mark = dim.Render(" (current)")
		}
		if i == m.bpick {
			L = append(L, "  "+cyan.Render("› ")+b+mark)
		} else {
			L = append(L, "    "+dim.Render(b)+mark)
		}
	}
	L = append(L, "", "  "+dim.Render("↑↓ pick · ⏎ trigger · esc back"))
	return strings.Join(L, "\n")
}

// workflowContent returns the WORKFLOWS section lines (args for the caller's add()).
func (m model) workflowContent(frame string) []string {
	if len(m.workflows) == 0 {
		return nil
	}
	wh := fhdr("WORKFLOWS", m.folds[3], "4")
	if m.focus == 1 {
		wh += "  " + cyan.Render("◀")
	}
	c := []string{wh}
	if !m.folds[3] {
		for i, wf := range m.workflows {
			if i >= 10 {
				c = append(c, "  "+dim.Render(fmt.Sprintf("… +%d more", len(m.workflows)-10)))
				break
			}
			st := green
			if wf.State != "active" {
				st = dim
			}
			badge := m.runBadge(wf.ID, frame)
			if m.focus == 1 && i == m.wfpick {
				c = append(c, "  "+cyan.Render("› ")+st.Render("●")+" "+wf.Name+badge)
			} else {
				c = append(c, "    "+st.Render("●")+" "+dim.Render(wf.Name)+badge)
			}
		}
		c = append(c, "  "+dim.Render("⏎ run · v watch · tab back to files"))
	}
	return c
}

func (m model) View() string {
	if m.prPick {
		return m.prPickerView()
	}
	if m.wfBranch {
		return m.wfBranchView()
	}
	if m.notesMode {
		return m.notesView()
	}
	if m.picking {
		return m.pickerView()
	}
	frame := m.sp.View()
	if !m.loaded {
		return "\n  " + cyan.Render(frame) + " " + dim.Render("loading PR…")
	}
	s := m.status
	if !s.Repo {
		return "\n  " + dim.Render("not a git repository")
	}
	if s.PR == nil {
		L := []string{"", "  " + bold.Render(s.Branch), "", "  " + dim.Render("no open PR for this branch") + "  " + dim.Render("· p review other PRs")}
		if wc := m.workflowContent(frame); len(wc) > 0 {
			L = append(L, "")
			for _, ln := range wc {
				L = append(L, "  "+ln)
			}
		}
		return strings.Join(L, "\n")
	}
	p := s.PR
	w := m.width - 4
	if w > 100 {
		w = 100
	}
	if w < 40 {
		w = 76
	}

	var L []string
	add := func(t string) { L = append(L, "  "+t) }
	blank := func() { L = append(L, "") }

	pillBg := "28"
	if p.State == "MERGED" {
		pillBg = "90"
	} else if p.State == "CLOSED" {
		pillBg = "124"
	}
	pill := lipgloss.NewStyle().Background(lipgloss.Color(pillBg)).Foreground(lipgloss.Color("15")).Bold(true).Padding(0, 1).Render(p.State)
	review := ""
	if p.State == "OPEN" {
		switch p.ReviewDecision {
		case "REVIEW_REQUIRED":
			review = "review required"
		case "APPROVED":
			review = "approved"
		case "CHANGES_REQUESTED":
			review = "changes requested"
		}
	}
	if m.viewNum > 0 {
		add(mauve.Render("▶ viewing PR #"+strconv.Itoa(m.viewNum)) + dim.Render("  · esc to return to your PR"))
		blank()
	}
	head := pill + " " + bold.Render(fmt.Sprintf("#%d", p.Number)) + " " + dim.Render(s.Branch)
	if review != "" {
		head += dim.Render("   · " + review)
	}
	add(head)
	if p.Title != "" {
		add(bold.Render(trunc(p.Title, w)))
	}

	if p.State == "OPEN" {
		sm := ciSummary(p.Checks)
		n := len(p.Checks)
		var hl string
		switch sm.Overall {
		case "fail":
			hl = red.Bold(true).Render("✗ CI failing") + " " + dim.Render(fmt.Sprintf("· %d/%d failed", sm.Fail, n))
		case "pending":
			hl = yellow.Bold(true).Render(frame+" CI running") + " " + dim.Render(fmt.Sprintf("· %d/%d left", sm.Pending, n))
		case "pass":
			hl = green.Bold(true).Render("✓ CI passing") + " " + dim.Render(fmt.Sprintf("· %d checks", n))
		default:
			hl = dim.Render("no checks reported")
		}
		blank()
		add(hl)
	}

	blank()
	chips := dim.Render("none")
	if len(p.Labels) > 0 {
		var parts []string
		for _, l := range p.Labels {
			c := l.Color
			if c == "" {
				c = "888888"
			}
			parts = append(parts, lipgloss.NewStyle().Foreground(lipgloss.Color("#"+c)).Render("●")+" "+l.Name)
		}
		chips = strings.Join(parts, "  ")
	}
	add(dim.Render("labels") + "  " + chips)
	who := dim.Render("unassigned")
	if len(p.Assignees) > 0 {
		var as []string
		for _, a := range p.Assignees {
			as = append(as, "@"+a.Login)
		}
		who = strings.Join(as, " ")
	}
	add(dim.Render("diff") + "    " + green.Render(fmt.Sprintf("+%d", p.Additions)) + " " + red.Render(fmt.Sprintf("-%d", p.Deletions)) + " " + dim.Render("·") + fmt.Sprintf(" %d files ", p.ChangedFiles) + dim.Render("·") + " " + who)

	if body := descLines(p.Body, 8, w-8); len(body) > 0 {
		blank()
		add(fhdr("DESCRIPTION", m.folds[0], "1"))
		if !m.folds[0] {
			box := lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(lipgloss.Color("238")).Padding(0, 1).Render(strings.Join(body, "\n"))
			for _, ln := range strings.Split(box, "\n") {
				add(ln)
			}
		}
	}

	blank()
	if p.State == "OPEN" {
		add(fhdr("CHECKS", m.folds[1], "2"))
	} else {
		add(fhdr("STATUS", m.folds[1], "2"))
	}
	if !m.folds[1] {
		switch {
		case p.State == "MERGED":
			add(mauve.Render(" merged"))
		case p.State == "CLOSED":
			add(red.Render("✗ closed"))
		case len(p.Checks) == 0:
			add("  " + dim.Render("no checks reported yet"))
		default:
			cs := append([]Check{}, p.Checks...)
			rank := map[string]int{"fail": 0, "pending": 1, "pass": 2}
			rankOf := func(b string) int {
				if r, ok := rank[b]; ok {
					return r
				}
				return 3
			}
			sort.SliceStable(cs, func(i, j int) bool { return rankOf(cs[i].Bucket) < rankOf(cs[j].Bucket) })
			sm := ciSummary(p.Checks)
			pr := m.prog // aggregate CI bar, colored by state, inline on the top check
			switch sm.Overall {
			case "fail":
				pr.FullColor = "#f38ba8"
			case "pending":
				pr.FullColor = "#f9e2af"
			default:
				pr.FullColor = "#a6e3a1"
			}
			for i, c := range cs {
				ic, st := checkGlyph(c.Bucket, frame)
				row := "  " + st.Render(ic) + " " + c.Name
				if i == 0 {
					row += "   " + pr.View()
				}
				add(row)
			}
			add("  " + green.Render(fmt.Sprintf("%d pass", sm.Pass)) + " " + dim.Render("·") + " " + red.Render(fmt.Sprintf("%d fail", sm.Fail)) + " " + dim.Render("·") + " " + yellow.Render(fmt.Sprintf("%d pending", sm.Pending)))
		}
	}

	if len(p.Files) > 0 {
		blank()
		ff := m.filteredFiles()
		hdr := fhdr(fmt.Sprintf("FILES (%d)", p.ChangedFiles), m.folds[2], "3")
		if m.focus == 0 && len(m.workflows) > 0 {
			hdr += "  " + cyan.Render("◀")
		}
		if m.filtering {
			hdr += "   " + cyan.Render("/") + m.ti.View() + dim.Render(fmt.Sprintf("  %d", len(ff)))
		}
		add(hdr)
		if !m.folds[2] {
			const win = 14
			start := 0
			if m.cursor >= win {
				start = m.cursor - win + 1
			}
			end := start + win
			if end > len(ff) {
				end = len(ff)
			}
			aw, dw := 2, 2 // align the +/- columns so paths line up
			for _, f := range ff {
				if l := len(strconv.Itoa(f.Additions)) + 1; l > aw {
					aw = l
				}
				if l := len(strconv.Itoa(f.Deletions)) + 1; l > dw {
					dw = l
				}
			}
			for i := start; i < end; i++ {
				f := ff[i]
				stat := green.Render(lpad("+"+strconv.Itoa(f.Additions), aw)) + " " + red.Render(lpad("-"+strconv.Itoa(f.Deletions), dw))
				full := truncTail(f.Path, w-aw-dw-6)
				dir, base := "", full
				if idx := strings.LastIndex(full, "/"); idx >= 0 {
					dir, base = full[:idx+1], full[idx+1:]
				}
				icon := fileIcon(f.Path)
				if i == m.cursor {
					add(cyan.Render("›") + " " + stat + "  " + cyan.Render(icon) + " " + dim.Render(dir) + bold.Render(base))
				} else {
					add("  " + stat + "  " + dim.Render(icon+" "+dir) + base)
				}
			}
			if len(ff) == 0 {
				add("  " + dim.Render("no match"))
			} else if end < len(ff) {
				add("  " + dim.Render(fmt.Sprintf("… +%d more", len(ff)-end)))
			}
		}
	}

	if wc := m.workflowContent(frame); len(wc) > 0 {
		blank()
		for _, ln := range wc {
			add(ln)
		}
	}

	blank()
	cap := func(k string) string {
		return lipgloss.NewStyle().Background(lipgloss.Color("236")).Foreground(lipgloss.Color("252")).Bold(true).Padding(0, 1).Render(k)
	}
	openHint := cap("o") + dim.Render(" open") + "  " + cap("d") + dim.Render(" diff all")
	if m.viewNum > 0 {
		add(dim.Render("↑↓ files · ⏎ annotate (ga) · d diff · a approve · r review · c comment · o web · esc back"))
	} else if settled(s) {
		add(dim.Render(strings.ToLower(p.State)+" · not watching") + "    " + openHint)
	} else {
		btn := lipgloss.NewStyle().Background(lipgloss.Color("28")).Foreground(lipgloss.Color("15")).Bold(true).Padding(0, 1).Render(" Merge PR")
		add(cyan.Render(frame) + " " + dim.Render("live · 5s") + "    " + cap("m") + btn + "   " + openHint)
	}
	if m.viewNum == 0 && len(p.Files) > 0 {
		add(dim.Render("↑↓ pick · ⏎ review (ga) · / search · u update"))
		add(dim.Render("a notes · s send · p PRs · tab/w workflows · 1/2/3/4 fold"))
	}
	if m.updating {
		add(yellow.Render(frame + " updating branch with base…"))
	} else if m.sent != "" {
		add(green.Render("✓ " + m.sent))
	}

	return "\n" + strings.Join(L, "\n")
}

func lpad(s string, n int) string {
	if len(s) >= n {
		return s
	}
	return strings.Repeat(" ", n-len(s)) + s
}

var fileIcons = map[string]string{
	".rb": "", ".erb": "", ".go": "", ".js": "", ".mjs": "", ".ts": "", ".tsx": "", ".jsx": "",
	".py": "", ".rs": "", ".sh": "", ".md": "", ".json": "", ".yml": "", ".yaml": "",
	".html": "", ".css": "", ".scss": "", ".sql": "", ".lock": "", ".toml": "", ".vue": "﵂", ".rake": "",
}

func fileIcon(p string) string {
	if g, ok := fileIcons[strings.ToLower(filepath.Ext(p))]; ok {
		return g
	}
	return "" // generic file
}

func checkGlyph(bucket, frame string) (string, lipgloss.Style) {
	switch bucket {
	case "pass":
		return "✓", green
	case "fail":
		return "✗", red
	case "pending":
		return frame, yellow
	default:
		return "·", dim
	}
}

var (
	htmlComment = regexp.MustCompile(`(?s)<!--.*?-->`)
	mdBoldRe    = regexp.MustCompile(`\*\*([^*]+)\*\*|__([^_]+)__`)
	mdCodeRe    = regexp.MustCompile("`([^`]+)`")
	mdHeadRe    = regexp.MustCompile(`^\s*#+\s*(.*)`)
	mdBulletRe  = regexp.MustCompile(`^(\s*)[-*]\s+(.*)`)
	descBase    = lipgloss.NewStyle().Foreground(lipgloss.Color("250"))
	descHead    = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("44"))
)

func mdInline(s string) string {
	s = mdBoldRe.ReplaceAllStringFunc(s, func(m string) string { return bold.Render(strings.Trim(m, "*_")) })
	s = mdCodeRe.ReplaceAllStringFunc(s, func(m string) string { return cyan.Render(strings.Trim(m, "`")) })
	return s
}

func descLines(body string, max, width int) []string {
	body = htmlComment.ReplaceAllString(body, "")
	var out []string
	content := 0
	for _, raw := range strings.Split(strings.ReplaceAll(body, "\r", ""), "\n") {
		ln := strings.TrimRight(raw, " \t")
		if strings.TrimSpace(ln) == "" {
			if content > 0 && (len(out) == 0 || out[len(out)-1] != "") { // keep paragraph breaks
				out = append(out, "")
			}
			continue
		}
		ln = trunc(ln, width) // truncate plain, then style
		var styled string
		if h := mdHeadRe.FindStringSubmatch(ln); h != nil {
			styled = descHead.Render(mdInline(h[1]))
		} else if b := mdBulletRe.FindStringSubmatch(ln); b != nil {
			styled = b[1] + cyan.Render("•") + " " + descBase.Render(mdInline(b[2]))
		} else {
			styled = descBase.Render(mdInline(ln))
		}
		out = append(out, styled)
		if content++; content >= max {
			break
		}
	}
	for len(out) > 0 && out[len(out)-1] == "" {
		out = out[:len(out)-1]
	}
	return out
}

func watchPane() {
	p := tea.NewProgram(newModel(ciCwd()), tea.WithAltScreen())
	_, _ = p.Run()
}
