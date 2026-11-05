// Package sporetoml reads one section at a time from a project's
// spore.toml. It covers the tiny TOML subset the kernel uses: bare or
// quoted scalar values, `# comment` lines, trailing comments, and blank
// lines. Callers own key validation and value typing.
package sporetoml

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Entry is one `key = value` line. Value has its trailing comment and
// surrounding quotes stripped.
type Entry struct {
	Line  int
	Key   string
	Value string
}

// ReadSection returns the entries of [section] in <projectRoot>/spore.toml.
// A missing file returns no entries and no error.
func ReadSection(projectRoot, section string) ([]Entry, error) {
	path := filepath.Join(projectRoot, "spore.toml")
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	entries, err := ParseSection(string(b), section)
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	return entries, nil
}

// ParseSection returns the entries of [section] in content. Lines in
// other sections are ignored; a line in [section] without `=` is an
// error so a misconfigured spore.toml surfaces loudly. Section "" is
// the top-level table, the lines before the first header.
func ParseSection(content, section string) ([]Entry, error) {
	var out []Entry
	in := section == ""
	scanner := bufio.NewScanner(strings.NewReader(content))
	for lineNum := 1; scanner.Scan(); lineNum++ {
		line := strings.TrimSpace(StripComment(scanner.Text()))
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			in = strings.TrimSpace(line[1:len(line)-1]) == section
			continue
		}
		if !in {
			continue
		}
		eq := strings.IndexByte(line, '=')
		if eq <= 0 {
			return nil, fmt.Errorf("line %d: malformed entry %q", lineNum, line)
		}
		out = append(out, Entry{
			Line:  lineNum,
			Key:   strings.TrimSpace(line[:eq]),
			Value: StripQuotes(strings.TrimSpace(line[eq+1:])),
		})
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// Bool parses e.Value as a TOML boolean.
func (e Entry) Bool() (bool, error) {
	switch e.Value {
	case "true":
		return true, nil
	case "false":
		return false, nil
	}
	return false, fmt.Errorf("line %d: %s: want true or false, got %q", e.Line, e.Key, e.Value)
}

// StripComment drops a trailing `# comment`, ignoring `#` inside quotes.
func StripComment(line string) string {
	inQuote := byte(0)
	for i := 0; i < len(line); i++ {
		ch := line[i]
		switch {
		case inQuote != 0:
			if ch == inQuote {
				inQuote = 0
			}
		case ch == '"' || ch == '\'':
			inQuote = ch
		case ch == '#':
			return line[:i]
		}
	}
	return line
}

// StripQuotes removes one pair of matching surrounding quotes.
func StripQuotes(v string) string {
	if len(v) >= 2 {
		first, last := v[0], v[len(v)-1]
		if (first == '"' && last == '"') || (first == '\'' && last == '\'') {
			return v[1 : len(v)-1]
		}
	}
	return v
}
