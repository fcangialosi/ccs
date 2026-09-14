package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestRealPrompt(t *testing.T) {
	real := []string{"help me fix the bug", "  what is 2+2?  ", "Review PR 42"}
	fake := []string{"", "   ", "<command-name>foo</command-name>", "<local-command-caveat>...", "Caveat: The messages below were generated"}
	for _, s := range real {
		if !realPrompt(s) {
			t.Errorf("realPrompt(%q) = false, want true", s)
		}
	}
	for _, s := range fake {
		if realPrompt(s) {
			t.Errorf("realPrompt(%q) = true, want false", s)
		}
	}
}

// A session file with a custom title, a last-prompt record, a real user prompt,
// a slash-command user message, and a tool-result user message.
func writeSession(t *testing.T, dir string) string {
	t.Helper()
	lines := []string{
		`{"type":"user","sessionId":"s1","cwd":"/home/x/proj","gitBranch":"main","timestamp":"2026-01-01T10:00:00Z","message":{"content":"first real question"}}`,
		`{"type":"assistant","timestamp":"2026-01-01T10:00:05Z","message":{"content":"answer"}}`,
		`{"type":"custom-title","customTitle":"my session"}`,
		`{"type":"user","sessionId":"s1","timestamp":"2026-01-01T10:05:00Z","message":{"content":"<command-name>clear</command-name>"}}`,
		`{"type":"user","sessionId":"s1","timestamp":"2026-01-01T10:06:00Z","message":{"content":[{"type":"tool_result","content":"big output"}]}}`,
		`{"type":"user","sessionId":"s1","timestamp":"2026-01-01T10:10:00Z","message":{"content":"the LAST real thing I said"}}`,
		`{"type":"last-prompt","lastPrompt":"the LAST real thing I said","sessionId":"s1"}`,
	}
	path := filepath.Join(dir, "s1.jsonl")
	body := ""
	for _, l := range lines {
		body += l + "\n"
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestExtractMeta(t *testing.T) {
	path := writeSession(t, t.TempDir())
	m := extractMeta(path)
	if m.customTitle != "my session" {
		t.Errorf("customTitle = %q", m.customTitle)
	}
	if m.lastPrompt() != "the LAST real thing I said" {
		t.Errorf("lastPrompt = %q", m.lastPrompt())
	}
	// Last interacted must be the last REAL user message (10:10), not the
	// tool-result (10:06) or slash-command (10:05).
	want, _ := time.Parse(time.RFC3339, "2026-01-01T10:10:00Z")
	if !m.lastMsg.Equal(want) {
		t.Errorf("lastMsg = %v, want %v", m.lastMsg, want)
	}
}

func TestLoadFromJSONL(t *testing.T) {
	path := writeSession(t, t.TempDir())
	it := loadFromJSONL(path, "/home/x", nil)
	if it == nil {
		t.Fatal("loadFromJSONL returned nil")
	}
	if it.session.FirstPrompt != "first real question" {
		t.Errorf("FirstPrompt = %q", it.session.FirstPrompt)
	}
	if it.session.LastPrompt != "the LAST real thing I said" {
		t.Errorf("LastPrompt = %q", it.session.LastPrompt)
	}
	created, _ := time.Parse(time.RFC3339, "2026-01-01T10:00:00Z")
	lastMsg, _ := time.Parse(time.RFC3339, "2026-01-01T10:10:00Z")
	if !it.session.Created.Equal(created) {
		t.Errorf("Created = %v, want %v", it.session.Created, created)
	}
	if !it.session.LastMsg.Equal(lastMsg) {
		t.Errorf("LastMsg = %v, want %v", it.session.LastMsg, lastMsg)
	}
}

func TestSortModes(t *testing.T) {
	tOld, _ := time.Parse(time.RFC3339, "2026-01-01T00:00:00Z")
	tMid, _ := time.Parse(time.RFC3339, "2026-02-01T00:00:00Z")
	tNew, _ := time.Parse(time.RFC3339, "2026-03-01T00:00:00Z")

	// A: created old, interacted recently. B: created recently, interacted mid.
	a := item{session: Session{SessionID: "A", Created: tOld, LastMsg: tNew}}
	b := item{session: Session{SessionID: "B", Created: tNew, LastMsg: tMid}}
	items := []item{b, a}

	sortItems(items, sortRecent)
	if items[0].session.SessionID != "A" {
		t.Errorf("sortRecent: first = %s, want A (interacted most recently)", items[0].session.SessionID)
	}
	sortItems(items, sortCreated)
	if items[0].session.SessionID != "B" {
		t.Errorf("sortCreated: first = %s, want B (created most recently)", items[0].session.SessionID)
	}
}

func TestOneLine(t *testing.T) {
	if got := oneLine("hello\n  world\t!", 100); got != "hello world !" {
		t.Errorf("oneLine collapse = %q", got)
	}
	if got := oneLine("abcdefghij", 8); got != "abcde..." {
		t.Errorf("oneLine truncate = %q (len %d)", got, len(got))
	}
}
