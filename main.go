package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/list"
	"github.com/charmbracelet/bubbles/textinput"
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
	jsonlPath    string
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
	scanningStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("214"))
)

// ── Key bindings ────────────────────────────────────────────────────

type keyMap struct {
	search       key.Binding
	toggleAll    key.Binding
	toggleActive key.Binding
}

var keys = keyMap{
	search: key.NewBinding(
		key.WithKeys("/", "ctrl+f"),
		key.WithHelp("/", "search names+contents"),
	),
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

	searchInput    textinput.Model
	searching      bool            // input is focused
	searchSeq      int             // bumped on every query change; tags async scans
	scanning       bool            // a background content scan is in flight
	contentQuery   string          // query the current contentMatches correspond to
	contentMatches map[string]bool // session IDs whose JSONL contents match contentQuery
}

// debounceMsg fires a short while after the query stops changing; if its seq is
// still current we launch the content scan.
type debounceMsg struct {
	seq   int
	query string
}

// scanResultMsg carries the result of a background content scan.
type scanResultMsg struct {
	seq     int
	query   string
	matches map[string]bool
}

const searchDebounce = 200 * time.Millisecond

func debounceCmd(seq int, query string) tea.Cmd {
	return tea.Tick(searchDebounce, func(time.Time) tea.Msg {
		return debounceMsg{seq: seq, query: query}
	})
}

func scanCmd(items []item, seq int, query string) tea.Cmd {
	return func() tea.Msg {
		return scanResultMsg{seq: seq, query: query, matches: searchContents(items, query)}
	}
}

func (m model) Init() tea.Cmd { return nil }

func (m model) query() string { return strings.TrimSpace(m.searchInput.Value()) }

func (m model) applyFilters() model {
	terms := strings.Fields(strings.ToLower(m.query()))
	// Content matches only count when they were computed for the live query;
	// while typing they lag behind until the debounced scan catches up.
	contentCurrent := m.contentMatches != nil && m.contentQuery == m.query()

	var filtered []list.Item
	for _, it := range m.allItems {
		if m.namedOnly && it.session.Name == "" {
			continue
		}
		if m.activeOnly && !it.session.Active {
			continue
		}
		if len(terms) > 0 {
			meta := metaContainsAll(it, terms)
			content := contentCurrent && m.contentMatches[it.session.SessionID]
			if !meta && !content {
				continue
			}
		}
		filtered = append(filtered, it)
	}
	m.list.SetItems(filtered)
	return m
}

// metaContainsAll reports whether every term appears somewhere in the session's
// metadata (name, summary, first prompt, path, branch), case-insensitively.
func metaContainsAll(it item, termsLower []string) bool {
	hay := strings.ToLower(strings.Join([]string{
		it.session.Name,
		it.session.Summary,
		it.session.FirstPrompt,
		it.projectShort,
		it.session.ProjectPath,
		it.session.GitBranch,
	}, " "))
	for _, t := range termsLower {
		if !strings.Contains(hay, t) {
			return false
		}
	}
	return true
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		if m.searching {
			return m.updateSearching(msg)
		}

		switch {
		case key.Matches(msg, keys.search):
			m.searching = true
			m.searchInput.Focus()
			return m, textinput.Blink
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
		case "q", "ctrl+c":
			m.quitting = true
			return m, tea.Quit
		case "esc":
			if m.query() != "" {
				m = m.clearSearch()
				return m, nil
			}
			m.quitting = true
			return m, tea.Quit
		}

	case debounceMsg:
		// Only launch the scan if no newer keystroke has arrived since.
		if msg.seq == m.searchSeq && strings.TrimSpace(msg.query) != "" {
			return m, scanCmd(m.allItems, msg.seq, msg.query)
		}
		return m, nil

	case scanResultMsg:
		if msg.seq == m.searchSeq {
			m.contentMatches = msg.matches
			m.contentQuery = strings.TrimSpace(msg.query)
			m.scanning = false
			m = m.applyFilters()
		}
		return m, nil

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.list.SetSize(msg.Width, msg.Height-2)
		m.searchInput.Width = msg.Width - 12
	}
	var cmd tea.Cmd
	m.list, cmd = m.list.Update(msg)
	return m, cmd
}

// updateSearching handles keys while the search input is focused.
func (m model) updateSearching(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "enter":
		// Resume whatever is highlighted in the (live-filtered) list.
		if sel, ok := m.list.SelectedItem().(item); ok {
			m.selected = &sel
			m.quitting = true
			return m, tea.Quit
		}
		return m, nil
	case "esc":
		m = m.clearSearch()
		m.searching = false
		m.searchInput.Blur()
		return m, nil
	case "ctrl+c":
		m.quitting = true
		return m, tea.Quit
	case "up", "down", "pgup", "pgdown":
		// Navigate the results without leaving the search box.
		var cmd tea.Cmd
		m.list, cmd = m.list.Update(msg)
		return m, cmd
	}

	prev := m.searchInput.Value()
	var cmd tea.Cmd
	m.searchInput, cmd = m.searchInput.Update(msg)
	cmds := []tea.Cmd{cmd}

	if m.searchInput.Value() != prev {
		m.searchSeq++
		if m.query() == "" {
			m.scanning = false
			m.contentMatches = nil
			m.contentQuery = ""
		} else {
			m.scanning = true
			cmds = append(cmds, debounceCmd(m.searchSeq, m.searchInput.Value()))
		}
		m = m.applyFilters() // instant metadata narrowing; contents stream in later
	}
	return m, tea.Batch(cmds...)
}

// clearSearch drops the active query and any content matches, then re-filters.
func (m model) clearSearch() model {
	m.searchInput.SetValue("")
	m.searchSeq++
	m.scanning = false
	m.contentMatches = nil
	m.contentQuery = ""
	return m.applyFilters()
}

func (m model) View() string {
	if m.quitting {
		return ""
	}

	count := len(m.list.Items())

	var bottom string
	if m.searching {
		status := ""
		if m.scanning {
			status = scanningStyle.Render("  ⋯ scanning contents")
		}
		bottom = statusBarStyle.Render(m.searchInput.View()) + status +
			statusBarStyle.Render(fmt.Sprintf("  │  %d", count))
	} else {
		namedLabel := filterToggle("named", m.namedOnly)
		activeLabel := filterToggle("active", m.activeOnly)
		left := fmt.Sprintf("%s  %s", namedLabel, activeLabel)
		if q := m.query(); q != "" {
			left += "  " + filterOnStyle.Render("search: "+q)
		}
		bottom = statusBarStyle.Render(fmt.Sprintf("%s  │  %d sessions", left, count))
	}

	return m.list.View() + "\n" + bottom
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
			indexed[idx].jsonlPath = jf
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
		jsonlPath:    path,
	}
}

// searchContents scans every session's JSONL for the query, in parallel across
// CPUs. A session matches if all whitespace-separated terms appear somewhere in
// its file (case-insensitive). Returns the set of matching session IDs.
func searchContents(items []item, query string) map[string]bool {
	terms := strings.Fields(query)
	if len(terms) == 0 {
		return nil
	}
	needles := make([][]byte, len(terms))
	for i, t := range terms {
		needles[i] = asciiLower([]byte(t))
	}

	matches := make(map[string]bool)
	var mu sync.Mutex
	var wg sync.WaitGroup
	sem := make(chan struct{}, runtime.NumCPU())

	for _, it := range items {
		if it.jsonlPath == "" {
			continue
		}
		wg.Add(1)
		sem <- struct{}{}
		go func(it item) {
			defer wg.Done()
			defer func() { <-sem }()
			if fileContainsAll(it.jsonlPath, needles) {
				mu.Lock()
				matches[it.session.SessionID] = true
				mu.Unlock()
			}
		}(it)
	}
	wg.Wait()
	return matches
}

// fileContainsAll reports whether every needle (already ASCII-lowercased)
// appears somewhere in the file. It reads in bounded chunks and carries a small
// overlap so matches spanning a chunk boundary are still found — this avoids
// the old bufio.Scanner buffer limit that silently skipped >1MB JSONL lines.
func fileContainsAll(path string, needles [][]byte) bool {
	if len(needles) == 0 {
		return true
	}
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()

	maxLen := 0
	for _, n := range needles {
		if len(n) > maxLen {
			maxLen = len(n)
		}
	}
	overlap := maxLen - 1
	if overlap < 0 {
		overlap = 0
	}

	found := make([]bool, len(needles))
	remaining := len(needles)

	r := bufio.NewReaderSize(f, 1<<20)
	chunk := make([]byte, 1<<20)
	window := make([]byte, 0, (1<<20)+overlap)

	for {
		n, err := r.Read(chunk)
		if n > 0 {
			asciiLowerInPlace(chunk[:n])
			window = append(window, chunk[:n]...)
			for i, nd := range needles {
				if !found[i] && bytes.Contains(window, nd) {
					found[i] = true
					remaining--
				}
			}
			if remaining == 0 {
				return true
			}
			if len(window) > overlap {
				copy(window, window[len(window)-overlap:])
				window = window[:overlap]
			}
		}
		if err != nil {
			return false
		}
	}
}

// asciiLowerInPlace lowercases ASCII A-Z in b, leaving UTF-8 multibyte
// sequences (bytes >= 0x80) untouched.
func asciiLowerInPlace(b []byte) {
	for i, c := range b {
		if c >= 'A' && c <= 'Z' {
			b[i] = c + ('a' - 'A')
		}
	}
}

func asciiLower(b []byte) []byte {
	out := make([]byte, len(b))
	copy(out, b)
	asciiLowerInPlace(out)
	return out
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
  / or ctrl+f      Search names + contents (type to filter)
  ↑/↓              Move selection (works while searching)
  tab              Toggle named / all sessions
  ctrl+a           Toggle active-only
  enter            Select / resume session
  esc              Clear search / quit
  q                Quit`)
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
	// Built-in filtering is disabled: it highlights matches by splicing style
	// codes into our already-styled Title strings, which corrupts the render.
	// We do our own combined name+content search instead.
	l.SetFilteringEnabled(false)
	l.SetShowHelp(true)
	l.AdditionalShortHelpKeys = func() []key.Binding {
		return []key.Binding{keys.search, keys.toggleAll, keys.toggleActive}
	}

	ti := textinput.New()
	ti.Placeholder = "search session contents..."
	ti.Prompt = "search: "
	ti.CharLimit = 200
	ti.Width = 60

	m := model{
		list:        l,
		allItems:    allItems,
		namedOnly:   namedOnly,
		searchInput: ti,
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
