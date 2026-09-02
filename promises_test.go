package main

// The promises on the front of the README, tested by reading the source.
//
// This is pointed at archives nobody trusts, so the one that matters is
// that it never unpacks one. Every promise here could be broken by a change
// that compiles and passes everything else.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func sources(t *testing.T) map[string]string {
	t.Helper()
	names, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	for _, name := range names {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		body, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		out[name] = string(body)
	}
	if len(out) < 6 {
		t.Fatalf("only found %d source files, so this is not looking at the program", len(out))
	}
	return out
}

func TestItNeverUnpacksAnything(t *testing.T) {
	forbidden := []string{
		"os.WriteFile", "os.Create", "os.Remove", "os.RemoveAll", "os.Rename",
		"os.Mkdir", "os.MkdirAll", "os.MkdirTemp", "os.Truncate", "os.Chmod",
		"os.OpenFile", "os.Symlink", "os.Link", "io.Copy",
	}
	for name, body := range sources(t) {
		for _, call := range forbidden {
			if strings.Contains(body, call) {
				t.Errorf("%s calls %s, and this program never writes anything out", name, call)
			}
		}
	}
}

func TestItNeverRunsAnythingOrOpensASocket(t *testing.T) {
	for name, body := range sources(t) {
		for _, bad := range []string{"os/exec", "exec.Command", `"net"`, `"net/http"`} {
			if strings.Contains(body, bad) {
				t.Errorf("%s has %s in it", name, bad)
			}
		}
	}
}

func TestNoDependencies(t *testing.T) {
	body, err := os.ReadFile("go.mod")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(body), "require") {
		t.Errorf("go.mod has a dependency in it:\n%s", body)
	}
}

func TestEveryEntryInTheListsSaysWhy(t *testing.T) {
	for name, list := range map[string][]Place{"grave": grave, "tagalong": tagalong} {
		if len(list) == 0 {
			t.Errorf("%s is empty", name)
		}
		for _, p := range list {
			if strings.TrimSpace(p.Match) == "" || strings.TrimSpace(p.What) == "" {
				t.Errorf("%s has an entry that does not say what it is", name)
			}
			if strings.TrimSpace(p.Why) == "" {
				t.Errorf("%s: %q does not say why anybody should care", name, p.Match)
			}
		}
	}
}

// The report has to say what it could not read, or a clean answer means
// nothing.
func TestGapsReachTheReport(t *testing.T) {
	got := &Tally{Name: "x.zip", Kind: "a zip", Gaps: []string{"the central directory ends in the middle of a record"}}
	if !strings.Contains(readOf(t, got, false), "could not be read") {
		t.Error("a gap did not reach the report")
	}
}

// An archive is somebody else's file and it is allowed to be nonsense.
// What it must not do is hang or fall over.
func TestBrokenInputStillFinishes(t *testing.T) {
	tidy := zipOf(t, []made{entry("a.txt", "a")}, "", "")
	for _, body := range [][]byte{
		tidy[:len(tidy)/2],
		tidy[len(tidy)/2:],
		append(tidy[:len(tidy)-4], 0xff, 0xff, 0xff, 0xff),
		filled(0, 4096),
		filled('P', 4096),
	} {
		path := filepath.Join(t.TempDir(), "odd.zip")
		if err := os.WriteFile(path, body, 0o644); err != nil {
			t.Fatal(err)
		}
		if got, err := look(path); err == nil {
			readOf(t, got, true)
		}
	}
}

func filled(b byte, n int) []byte {
	out := make([]byte, n)
	for i := range out {
		out[i] = b
	}
	return out
}

func TestReadmeMatchesTheFlags(t *testing.T) {
	body, err := os.ReadFile("README.md")
	if err != nil {
		t.Skip("no README yet")
	}
	readme := string(body)
	for _, flag := range []string{"--all", "--version"} {
		if !strings.Contains(readme, flag) {
			t.Errorf("%s is a flag and the README does not mention it", flag)
		}
	}
	if !strings.Contains(strings.ToLower(readme), "never extracts") {
		t.Error("the README no longer says that it never extracts anything")
	}
}
