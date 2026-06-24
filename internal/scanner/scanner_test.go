package scanner

import (
	"os"
	"path/filepath"
	"sort"
	"testing"
)

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestScanMatchesAndMaps(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "my-project", ".env"), "A=1")
	writeFile(t, filepath.Join(root, "api", ".env.production"), "B=2")
	writeFile(t, filepath.Join(root, "api", ".env.local"), "C=3")
	// Should be ignored:
	writeFile(t, filepath.Join(root, "web", "node_modules", ".env"), "D=4")
	writeFile(t, filepath.Join(root, "web", "README.md"), "not an env file")

	s := New(root, []string{"node_modules", ".git"}, []string{".env", ".env.*"})
	found, err := s.Scan()
	if err != nil {
		t.Fatalf("scan: %v", err)
	}

	got := make([]string, 0, len(found))
	for _, f := range found {
		got = append(got, f.Backup)
	}
	sort.Strings(got)

	want := []string{"api/.env.local", "api/.env.production", "my-project/.env"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v, want %v", got, want)
		}
	}
}

func TestHashFileChangesWithContent(t *testing.T) {
	p := filepath.Join(t.TempDir(), ".env")
	writeFile(t, p, "TOKEN=abc")
	h1, err := HashFile(p)
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, p, "TOKEN=xyz")
	h2, err := HashFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if h1 == h2 {
		t.Fatal("hash should change when content changes")
	}
}
