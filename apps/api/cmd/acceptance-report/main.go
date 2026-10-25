// Command acceptance-report reads `go test -json` of the acceptance suite and
// writes a Markdown table linking each PRD criterion to its test and status.
// It exits with 1 when a criterion has no passing test.
package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/rudolfoborges/lastro/apps/api/internal/acceptance"
)

type event struct {
	Action string `json:"Action"`
	Test   string `json:"Test"`
	Output string `json:"Output"`
}

type result struct {
	test   string
	status string
	output []string
}

func main() {
	out := flag.String("out", "", "also write the report to this file")
	flag.Parse()
	results, err := read(os.Stdin)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	report, ok := render(results)
	fmt.Print(report)
	if *out != "" {
		if err := write(*out, report); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(2)
		}
	}
	if !ok {
		os.Exit(1)
	}
}

func read(r io.Reader) (map[int]*result, error) {
	results := map[int]*result{}
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 1024*1024), 1024*1024)
	for scanner.Scan() {
		var e event
		if err := json.Unmarshal(scanner.Bytes(), &e); err != nil {
			continue
		}
		top, _, _ := strings.Cut(e.Test, "/")
		number, ok := acceptance.NumberOf(top)
		if !ok {
			continue
		}
		r := results[number]
		if r == nil {
			r = &result{test: top, status: "missing"}
			results[number] = r
		}
		switch {
		case e.Action == "output" && e.Test == top:
			r.output = append(r.output, e.Output)
		case e.Test == top && (e.Action == "pass" || e.Action == "fail" || e.Action == "skip"):
			r.status = e.Action
		}
	}
	return results, scanner.Err()
}

func render(results map[int]*result) (string, bool) {
	var b strings.Builder
	ok := true
	passed := 0
	b.WriteString("| # | Criterion (PRD §12) | Test | Status |\n| --- | --- | --- | --- |\n")
	for _, c := range acceptance.Criteria {
		r := results[c.Number]
		test, status := acceptance.TestPrefix(c.Number)+"…", "missing"
		if r != nil {
			test, status = r.test, r.status
		}
		if status == "pass" {
			passed++
		} else {
			ok = false
		}
		fmt.Fprintf(&b, "| %d | %s | `%s` | %s |\n", c.Number, c.Title, test, label(status))
	}
	header := fmt.Sprintf("## Acceptance criteria\n\n%d of %d criteria pass.\n\n", passed, len(acceptance.Criteria))
	for _, c := range acceptance.Criteria {
		if r := results[c.Number]; r != nil && r.status == "fail" {
			fmt.Fprintf(&b, "\n<details><summary>%s</summary>\n\n```\n%s```\n</details>\n", r.test, strings.Join(r.output, ""))
		}
	}
	return header + b.String(), ok
}

func label(status string) string {
	switch status {
	case "pass":
		return "✅ pass"
	case "fail":
		return "❌ fail"
	case "skip":
		return "⚠️ skipped"
	default:
		return "❌ missing"
	}
}

func write(path, report string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return fmt.Errorf("create report directory: %w", err)
	}
	if err := os.WriteFile(path, []byte(report), 0o600); err != nil {
		return fmt.Errorf("write report: %w", err)
	}
	return nil
}
