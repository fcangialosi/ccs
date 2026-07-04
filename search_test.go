package main

import (
	"os"
	"path/filepath"
	"testing"
)

// writeJSONL writes a throwaway session file and returns an item pointing at it.
func writeJSONL(t *testing.T, dir, id, body string) item {
	t.Helper()
	path := filepath.Join(dir, id+".jsonl")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return item{
		session:   Session{SessionID: id},
		jsonlPath: path,
	}
}

func TestSearchContents(t *testing.T) {
	dir := t.TempDir()
	a := writeJSONL(t, dir, "a", `{"type":"user"}`+"\n"+`{"text":"debugging a Kubernetes OOMKilled pod"}`+"\n")
	b := writeJSONL(t, dir, "b", `{"text":"totally unrelated grocery list"}`+"\n")
	items := []item{a, b}

	// Case-insensitive single term.
	got := searchContents(items, "kubernetes")
	if !got["a"] || got["b"] {
		t.Fatalf("single term: got %v, want only a", got)
	}

	// Multi-term is AND across the whole file (terms need not be adjacent).
	got = searchContents(items, "oomkilled pod")
	if !got["a"] || got["b"] {
		t.Fatalf("multi term AND: got %v, want only a", got)
	}

	// A term present in neither -> no match.
	got = searchContents(items, "kubernetes grocery")
	if len(got) != 0 {
		t.Fatalf("cross-file terms should not match a single file: got %v", got)
	}

	// Empty query matches nothing (search cleared).
	if got = searchContents(items, "   "); got != nil {
		t.Fatalf("empty query: got %v, want nil", got)
	}
}

// A match spanning a read-chunk boundary must still be found.
func TestFileContainsAllChunkBoundary(t *testing.T) {
	dir := t.TempDir()
	needle := "SPLIT_TOKEN"
	const chunk = 1 << 20
	// Place the needle straddling the 1MB chunk boundary.
	pre := chunk - len(needle)/2
	body := make([]byte, 0, chunk+64)
	for i := 0; i < pre; i++ {
		body = append(body, 'x')
	}
	body = append(body, []byte(needle)...)
	body = append(body, []byte("\ntail\n")...)
	path := filepath.Join(dir, "big.jsonl")
	if err := os.WriteFile(path, body, 0o644); err != nil {
		t.Fatal(err)
	}
	if !fileContainsAll(path, [][]byte{asciiLower([]byte(needle))}) {
		t.Fatal("needle straddling chunk boundary was not found")
	}
}

func TestMetaContainsAll(t *testing.T) {
	it := item{
		session:      Session{Name: "floorplan-est", FirstPrompt: "moving into a new apartment", GitBranch: "feature/x"},
		projectShort: "~/dev/apt",
	}
	if !metaContainsAll(it, []string{"floorplan"}) {
		t.Fatal("should match on name")
	}
	if !metaContainsAll(it, []string{"apartment", "apt"}) {
		t.Fatal("should match all terms across fields")
	}
	if metaContainsAll(it, []string{"kubernetes"}) {
		t.Fatal("should not match absent term")
	}
}
