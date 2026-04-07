package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/list"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// ── Data types ──────────────────────────────────────────────────────

type Session struct {
	SessionID   string
	Name        string // user-provided via --name / custom-title
	FirstPrompt string
	Summary     string
	MsgCount    int
	Created     time.Time
	Modified    time.Time
	GitBranch   string
	ProjectPath string
	Active      bool
}

type indexEntry struct {
	SessionID   string `json:"sessionId"`
	FirstPrompt string `json:"firstPrompt"`
	Summary     string `json:"summary"`
	MsgCount    int    `json:"messageCount"`
	Created     string `json:"created"`
	Modified    string `json:"modified"`
	GitBranch   string `json:"gitBranch"`
	ProjectPath string `json:"projectPath"`
	IsSidechain bool   `json:"isSidechain"`
}

type sessionsIndex struct {
	Entries []indexEntry `json:"entries"`
}

type jsonlEntry struct {
	Type        string          `json:"type"`
	SessionID   string          `json:"sessionId"`
	CWD         string          `json:"cwd"`
	GitBranch   string          `json:"gitBranch"`
	Timestamp   string          `json:"timestamp"`
	Message     json.RawMessage `json:"message"`
	CustomTitle string          `json:"customTitle"`
}

type msgContent struct {
	Content json.RawMessage `json:"content"`
}

type contentBlock struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type pidEntry struct {
	PID       int    `json:"pid"`
	SessionID string `json:"sessionId"`
	Name      string `json:"name"`
}

// ── List item ───────────────────────────────────────────────────────

type item struct {
	session      Session
	projectShort string
}

func (i item) Title() string {
	age := formatAge(i.session.Modified)
	active := ""
	if i.session.Active {
		active = activeStyle.Render(" ●")
	}

	if i.session.Name != "" {
		return fmt.Sprintf("%s  %s%s", ageStyle.Render(age), nameStyle.Render(i.session.Name), active)
	}

	summary := i.session.Summary
	if summary == "" {
		summary = i.session.FirstPrompt
		if len(summary) > 70 {
			summary = summary[:67] + "..."
		}
	}
	branch := ""
	if b := i.session.GitBranch; b != "" && b != "HEAD" && b != "main" && b != "master" {
		branch = "  " + branchStyle.Render(b)
	}
	return fmt.Sprintf("%s  %s%s%s", ageStyle.Render(age), titleStyle.Render(summary), branch, active)
}

func (i item) Description() string {
	parts := []string{pathStyle.Render(i.projectShort)}
	parts = append(parts, countStyle.Render(fmt.Sprintf("%d msgs", i.session.MsgCount)))

	desc := i.session.Summary
	if i.session.Name == "" || desc == "" {
		desc = i.session.FirstPrompt
	}
	if len(desc) > 80 {
		desc = desc[:77] + "..."
	}
	if desc != "" {
		parts = append(parts, promptStyle.Render(desc))
	}
	return strings.Join(parts, "  ")
}

func (i item) FilterValue() string {
	return i.session.Name + " " + i.session.Summary + " " + i.session.FirstPrompt + " " + i.session.ProjectPath + " " + i.session.GitBranch
}

// ── Styles ──────────────────────────────────────────────────────────

var (
	ageStyle       = lipgloss.NewStyle().Foreground(lipgloss.Color("243")).Width(10)
	nameStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color("99")).Bold(true)
	activeStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("42"))
	titleStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("252"))
	branchStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("213"))
	pathStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color("243"))
	countStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("243"))
	promptStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("245")).Italic(true)
	headerStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("99")).Bold(true).Padding(0, 1)
	statusBarStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("243")).Padding(0, 1)
	filterOnStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("42"))
	filterOffStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("243"))
)

// ── Key bindings ────────────────────────────────────────────────────

type keyMap struct {
	toggleAll    key.Binding
	toggleActive key.Binding
}

var keys = keyMap{
	toggleAll: key.NewBinding(
		key.WithKeys("tab"),
		key.WithHelp("tab", "named/all"),
	),
	toggleActive: key.NewBinding(
		key.WithKeys("ctrl+a"),
		key.WithHelp("ctrl+a", "active only"),
	),
}

// ── Model ───────────────────────────────────────────────────────────

type model struct {
	list       list.Model
	allItems   []item // full unfiltered set
	namedOnly  bool
	activeOnly bool
	selected   *item
	quitting   bool
	width      int
	height     int
}

func (m model) Init() tea.Cmd { return nil }

func (m model) applyFilters() model {
	var filtered []list.Item
	for _, it := range m.allItems {
		if m.namedOnly && it.session.Name == "" {
			continue
		}
		if m.activeOnly && !it.session.Active {
			continue
		}
		filtered = append(filtered, it)
	}
	m.list.SetItems(filtered)
	return m
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		if m.list.FilterState() != list.Filtering {
			switch {
			case key.Matches(msg, keys.toggleAll):
				m.namedOnly = !m.namedOnly
				m = m.applyFilters()
				return m, nil
			case key.Matches(msg, keys.toggleActive):
				m.activeOnly = !m.activeOnly
				m = m.applyFilters()
				return m, nil
			}

			switch msg.String() {
			case "enter":
				if sel, ok := m.list.SelectedItem().(item); ok {
					m.selected = &sel
					m.quitting = true
					return m, tea.Quit
				}
			case "q", "ctrl+c", "esc":
				m.quitting = true
				return m, tea.Quit
			}
		}
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.list.SetSize(msg.Width, msg.Height-2)
	}
	var cmd tea.Cmd
	m.list, cmd = m.list.Update(msg)
	return m, cmd
}

func (m model) View() string {
	if m.quitting {
		return ""
	}

	// Status bar with toggle indicators
	namedLabel := filterToggle("named", m.namedOnly)
	activeLabel := filterToggle("active", m.activeOnly)

	count := len(m.list.Items())
	status := statusBarStyle.Render(
		fmt.Sprintf("%s  %s  │  %d sessions", namedLabel, activeLabel, count),
	)

	return m.list.View() + "\n" + status
}

func filterToggle(label string, on bool) string {
	if on {
		return filterOnStyle.Render("▣ " + label)
	}
	return filterOffStyle.Render("▢ " + label)
}

// ── Data loading ────────────────────────────────────────────────────

func loadActiveSessions() map[string]string {
	home, _ := os.UserHomeDir()
	if home == "" {
		return nil
	}

	active := make(map[string]string)
	pidFiles, _ := filepath.Glob(filepath.Join(home, ".claude", "sessions", "*.json"))
	for _, pf := range pidFiles {
		data, err := os.ReadFile(pf)
		if err != nil {
			continue
		}
		var pe pidEntry
		if json.Unmarshal(data, &pe) != nil {
			continue
		}
		if pe.PID > 0 && syscall.Kill(pe.PID, 0) == nil {
			active[pe.SessionID] = pe.Name
		}
	}
	return active
}

func extractCustomTitle(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 256*1024), 256*1024)

	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) < 300 && strings.Contains(string(line), "custom-title") {
			var entry jsonlEntry
			if json.Unmarshal(line, &entry) == nil && entry.Type == "custom-title" && entry.CustomTitle != "" {
				return entry.CustomTitle
			}
		}
	}
	return ""
}

func loadAllSessions() []item {
	home, _ := os.UserHomeDir()
	if home == "" {
		return nil
	}

	activeSessions := loadActiveSessions()
	projectDirs, _ := filepath.Glob(filepath.Join(home, ".claude", "projects", "*"))

	var items []item
	for _, dir := range projectDirs {
		indexed := loadFromIndex(dir, home)
		indexedIDs := make(map[string]bool)
		for idx := range indexed {
			sid := indexed[idx].session.SessionID
			indexedIDs[sid] = true

			jf := filepath.Join(dir, sid+".jsonl")
			if title := extractCustomTitle(jf); title != "" {
				indexed[idx].session.Name = title
			}
			if _, ok := activeSessions[sid]; ok {
				indexed[idx].session.Active = true
				if name := activeSessions[sid]; name != "" && indexed[idx].session.Name == "" {
					indexed[idx].session.Name = name
				}
			}
			items = append(items, indexed[idx])
		}

		jsonls, _ := filepath.Glob(filepath.Join(dir, "*.jsonl"))
		for _, jf := range jsonls {
			sid := strings.TrimSuffix(filepath.Base(jf), ".jsonl")
			if indexedIDs[sid] {
				continue
			}
			if s := loadFromJSONL(jf, home, activeSessions); s != nil {
				items = append(items, *s)
			}
		}
	}

	sort.Slice(items, func(i, j int) bool {
		return items[i].session.Modified.After(items[j].session.Modified)
	})
	return items
}

func loadFromIndex(dir, home string) []item {
	data, err := os.ReadFile(filepath.Join(dir, "sessions-index.json"))
	if err != nil {
		return nil
	}
	var idx sessionsIndex
	if json.Unmarshal(data, &idx) != nil {
		return nil
	}

	var items []item
	for _, e := range idx.Entries {
		if e.IsSidechain || e.MsgCount < 2 {
			continue
		}
		mod, _ := time.Parse(time.RFC3339, e.Modified)
		cre, _ := time.Parse(time.RFC3339, e.Created)
		items = append(items, item{
			session: Session{
				SessionID:   e.SessionID,
				FirstPrompt: e.FirstPrompt,
				Summary:     e.Summary,
				MsgCount:    e.MsgCount,
				Created:     cre,
				Modified:    mod,
				GitBranch:   e.GitBranch,
				ProjectPath: e.ProjectPath,
			},
			projectShort: shortenPath(e.ProjectPath, home),
		})
	}
	return items
}

func loadFromJSONL(path, home string, activeSessions map[string]string) *item {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()

	var (
		sessionID   string
		customTitle string
		cwd         string
		branch      string
		firstPrompt string
		firstTS     time.Time
		lastTS      time.Time
		msgCount    int
	)

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 1024*1024), 1024*1024)

	for scanner.Scan() {
		var entry jsonlEntry
		if json.Unmarshal(scanner.Bytes(), &entry) != nil {
			continue
		}

		switch entry.Type {
		case "custom-title":
			if entry.CustomTitle != "" {
				customTitle = entry.CustomTitle
			}
		case "user":
			msgCount++
			if sessionID == "" {
				sessionID = entry.SessionID
				cwd = entry.CWD
				branch = entry.GitBranch
			}
			if ts, err := time.Parse(time.RFC3339, entry.Timestamp); err == nil {
				if firstTS.IsZero() {
					firstTS = ts
				}
				lastTS = ts
			}
			if firstPrompt == "" {
				firstPrompt = extractPromptText(entry.Message)
			}
		case "assistant":
			msgCount++
			if ts, err := time.Parse(time.RFC3339, entry.Timestamp); err == nil {
				lastTS = ts
			}
		}
	}

	if msgCount < 2 || sessionID == "" {
		return nil
	}

	if cwd == "" {
		dirName := filepath.Base(filepath.Dir(path))
		cwd = strings.ReplaceAll(strings.TrimPrefix(dirName, "-"), "-", "/")
		if !strings.HasPrefix(cwd, "/") {
			cwd = "/" + cwd
		}
	}

	active := false
	if _, ok := activeSessions[sessionID]; ok {
		active = true
		if customTitle == "" && activeSessions[sessionID] != "" {
			customTitle = activeSessions[sessionID]
		}
	}

	return &item{
		session: Session{
			SessionID:   sessionID,
			Name:        customTitle,
			FirstPrompt: firstPrompt,
			MsgCount:    msgCount,
			Created:     firstTS,
			Modified:    lastTS,
			GitBranch:   branch,
			ProjectPath: cwd,
			Active:      active,
		},
		projectShort: shortenPath(cwd, home),
	}
}

func extractPromptText(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var msg msgContent
	if json.Unmarshal(raw, &msg) != nil {
		return ""
	}
	var s string
	if json.Unmarshal(msg.Content, &s) == nil {
		return s
	}
	var blocks []contentBlock
	if json.Unmarshal(msg.Content, &blocks) == nil {
		for _, b := range blocks {
			if b.Type == "text" && b.Text != "" {
				return b.Text
			}
		}
	}
	return ""
}

func shortenPath(p, home string) string {
	if strings.HasPrefix(p, home) {
		return "~" + p[len(home):]
	}
	return p
}

func formatAge(t time.Time) string {
	if t.IsZero() {
		return "?"
	}
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	case d < 7*24*time.Hour:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	default:
		return t.Format("Jan 02")
	}
}

// ── Main ────────────────────────────────────────────────────────────

func main() {
	namedOnly := true // default: named only
	args := os.Args[1:]

	for len(args) > 0 {
		switch args[0] {
		case "-a", "--all":
			namedOnly = false
			args = args[1:]
		case "-l", "--list":
			allItems := loadAllSessions()
			for _, it := range allItems {
				if namedOnly && it.session.Name == "" {
					continue
				}
				name := it.session.Name
				if name == "" {
					name = displayTitle(it.session)
				}
				active := ""
				if it.session.Active {
					active = " ●"
				}
				fmt.Printf("%-10s %-30s %s (%s msgs)%s\n",
					formatAge(it.session.Modified),
					it.projectShort,
					name,
					strconv.Itoa(it.session.MsgCount),
					active,
				)
			}
			return
		case "-h", "--help":
			fmt.Println(`ccs - Claude Code Sessions

Browse and resume Claude Code sessions from any directory.

Usage:
  ccs              Named sessions (interactive picker)
  ccs -a           All sessions (interactive picker)
  ccs -l           List named sessions (plain text)
  ccs -a -l        List all sessions (plain text)
  ccs -h           Show this help

Keys:
  /                Start filtering
  tab              Toggle named / all sessions
  ctrl+a           Toggle active-only
  enter            Select session
  q, esc           Quit`)
			return
		case "-v", "--version":
			fmt.Println("ccs 0.1.0")
			return
		default:
			args = args[1:]
		}
	}

	allItems := loadAllSessions()
	if len(allItems) == 0 {
		fmt.Fprintln(os.Stderr, "No Claude sessions found.")
		os.Exit(1)
	}

	delegate := list.NewDefaultDelegate()
	delegate.Styles.SelectedTitle = delegate.Styles.SelectedTitle.
		Foreground(lipgloss.Color("255")).
		BorderForeground(lipgloss.Color("99"))
	delegate.Styles.SelectedDesc = delegate.Styles.SelectedDesc.
		Foreground(lipgloss.Color("245")).
		BorderForeground(lipgloss.Color("99"))

	l := list.New(nil, delegate, 80, 22)
	l.Title = "Claude Sessions"
	l.Styles.Title = headerStyle
	l.SetFilteringEnabled(true)
	l.SetShowHelp(true)
	l.AdditionalShortHelpKeys = func() []key.Binding {
		return []key.Binding{keys.toggleAll, keys.toggleActive}
	}

	m := model{
		list:      l,
		allItems:  allItems,
		namedOnly: namedOnly,
	}
	m = m.applyFilters()

	p := tea.NewProgram(m, tea.WithAltScreen())
	result, err := p.Run()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	final := result.(model)
	if final.selected == nil {
		return
	}

	s := final.selected.session

	dim := lipgloss.NewStyle().Foreground(lipgloss.Color("243"))

	fi, _ := os.Stdout.Stat()
	piped := fi != nil && (fi.Mode()&os.ModeCharDevice) == 0

	cmd := "cd " + s.ProjectPath + " && claude --resume " + s.SessionID

	if piped {
		fmt.Fprintln(os.Stderr)
		fmt.Fprintln(os.Stderr, dim.Render("  "+cmd))
		fmt.Fprintln(os.Stderr)
		fmt.Println(s.ProjectPath)
		fmt.Println(s.SessionID)
	} else {
		fmt.Println()
		fmt.Println(dim.Render("  " + cmd))
		fmt.Println()
	}
}

func displayTitle(s Session) string {
	if s.Summary != "" {
		return s.Summary
	}
	p := s.FirstPrompt
	if len(p) > 70 {
		p = p[:67] + "..."
	}
	return p
}
