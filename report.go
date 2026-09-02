package main

// What a finding is, and how it reaches the screen.

import (
	"fmt"
	"io"
	"sort"
	"strings"
)

type Severity int

const (
	// Note is worth knowing.
	Note Severity = iota
	// Carries is what is in it that nobody chose to put there: the weight,
	// the leftovers of the machine that built it, the credential somebody
	// swept up with the folder.
	Carries
	// Lands is where the files will actually end up, which is not always
	// where you are standing when you unpack them.
	Lands
	// Lies is the name or the listing not saying what it is. Nothing under
	// this can be taken at face value, because the archive has already
	// been shown to describe itself wrongly once.
	Lies
)

func (s Severity) tag() string {
	switch s {
	case Lies:
		return "LIES   "
	case Lands:
		return "LANDS  "
	case Carries:
		return "CARRIES"
	}
	return "NOTE   "
}

type Finding struct {
	Severity Severity
	Title    string
	Lines    []string
	// Advice is what somebody does about it. A finding without one is a
	// finding nobody acts on.
	Advice string
}

type Report struct {
	Findings []Finding
	// Gaps are what this run could not read. They print first, because
	// they change how everything under them should be taken.
	Gaps []string
}

func (r *Report) add(f Finding) { r.Findings = append(r.Findings, f) }

func (r *Report) gap(text string) {
	for _, seen := range r.Gaps {
		if seen == text {
			return
		}
	}
	r.Gaps = append(r.Gaps, text)
}

func (r *Report) worst() Severity {
	worst := Note
	for _, f := range r.Findings {
		if f.Severity > worst {
			worst = f.Severity
		}
	}
	return worst
}

const (
	indent = "             "
	wrap   = 62
)

func (r *Report) render(out io.Writer, title string) {
	fmt.Fprintf(out, "\n  %s\n  %s\n\n", title, strings.Repeat("=", min(len(title), wrap+13)))

	for _, g := range r.Gaps {
		block(out, "UNSEEN ", "Part of this could not be read", nil, g)
	}

	// Loudest first. Somebody who reads the first screen and stops should
	// have read the part that mattered.
	sorted := make([]Finding, len(r.Findings))
	copy(sorted, r.Findings)
	sort.SliceStable(sorted, func(i, j int) bool {
		return sorted[i].Severity > sorted[j].Severity
	})
	for _, f := range sorted {
		block(out, f.Severity.tag(), f.Title, f.Lines, f.Advice)
	}

	fmt.Fprintln(out, "    Nothing was unpacked. This only read the listing.")
	fmt.Fprintln(out)
}

func block(out io.Writer, tag, title string, lines []string, advice string) {
	// The title carries a file path often enough that it runs off the
	// side, so it wraps under itself. A path with no spaces in it still
	// goes over, and that is the right trade: breaking a path in half so
	// it fits makes it uncopyable.
	head := wrapText(title, wrap+10)
	if len(head) == 0 {
		head = []string{""}
	}
	fmt.Fprintf(out, "    [%s] %s\n", tag, head[0])
	for _, line := range head[1:] {
		fmt.Fprintf(out, "%s%s\n", indent, line)
	}
	for _, line := range lines {
		if line = strings.TrimRight(line, " "); line == "" {
			// A blank separator prints as a blank line and not as
			// thirteen spaces, so nothing here leaves trailing whitespace
			// in something somebody is going to paste elsewhere.
			fmt.Fprintln(out)
			continue
		}
		fmt.Fprintf(out, "%s%s\n", indent, line)
	}
	if advice != "" {
		if len(lines) > 0 {
			fmt.Fprintln(out)
		}
		for _, line := range wrapText(advice, wrap) {
			fmt.Fprintf(out, "%s%s\n", indent, line)
		}
	}
	fmt.Fprintln(out)
}

func wrapText(s string, width int) []string {
	words := strings.Fields(s)
	if len(words) == 0 {
		return nil
	}
	var lines []string
	line := words[0]
	for _, w := range words[1:] {
		if len(line)+1+len(w) > width {
			lines = append(lines, line)
			line = w
			continue
		}
		line += " " + w
	}
	return append(lines, line)
}

func plural(n int, one, many string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, one)
	}
	return fmt.Sprintf("%d %s", n, many)
}

// atMost keeps a list readable. Somebody with four hundred hosts in their
// history does not need all four hundred printed to take the point.
func atMost(lines []string, n int) []string {
	if len(lines) <= n {
		return lines
	}
	kept := make([]string, 0, n+1)
	kept = append(kept, lines[:n]...)
	return append(kept, fmt.Sprintf("... and %d more", len(lines)-n))
}

// columns lines a list up so the file and the thing found sit under each
// other, because a report with ragged columns gets skimmed rather than
// read. A path too long for its column drops to the next line instead of
// shoving everything sideways: the PSReadLine file is seventy characters
// on its own and one of those pushed every other row out of alignment.
const columnAt = 34

func columns(left, right string) []string {
	if right == "" {
		return []string{left}
	}
	// The right hand side wraps on its own and the padding is put back on
	// afterwards. Wrapping the finished line instead eats the padding,
	// because wrapping works on words and a run of spaces is not one, so
	// every row long enough to wrap lost its column.
	if len(left) >= columnAt {
		out := []string{left}
		for _, part := range wrapText(right, wrap+6) {
			out = append(out, "    "+part)
		}
		return out
	}
	pad := strings.Repeat(" ", columnAt)
	parts := wrapText(right, wrap+14-columnAt)
	if len(parts) == 0 {
		return []string{left}
	}
	out := []string{left + strings.Repeat(" ", columnAt-len(left)) + parts[0]}
	for _, part := range parts[1:] {
		out = append(out, pad+part)
	}
	return out
}

// folded puts a long sentence under a short label and keeps the second
// line under the first word rather than back at the margin.
func folded(label, text string) []string {
	wrapped := wrapText(text, wrap-len(label))
	out := make([]string, 0, len(wrapped))
	for i, line := range wrapped {
		if i == 0 {
			out = append(out, label+line)
			continue
		}
		out = append(out, strings.Repeat(" ", len(label))+line)
	}
	return out
}

// group is a list of two column rows that remembers how many rows it has,
// which is not the same as how many lines it prints: a path too long for
// its column takes two lines, and counting lines would have the report
// saying twelve files when it means nine.
type group struct {
	lines []string
	count int
}

func (g *group) add(left, right string) {
	g.lines = append(g.lines, columns(left, right)...)
	g.count++
}
