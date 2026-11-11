// Package dream turns spore-managed session transcripts into candidate
// harness improvements. This file finds and classifies the sessions;
// everything downstream consumes the Session values it returns.
package dream

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

type Kind string

const (
	KindCoordinator Kind = "coordinator"
	KindWorker      Kind = "worker"
)

// coordinatorMarker is the opening line of the shared coordinator role,
// which every coordinator session receives as its first user message.
// It is the only reliable way to tell a coordinator from an ad-hoc
// session, because both run with the project root as cwd.
const coordinatorMarker = "# Coordinator role"

// mintedDreamSlugRE is what task.Allocate makes of MintTask's
// "dream <run-id>" title for a CLI run id (YYYYMMDD-<4 hex>), including
// its -2..-99 collision suffix. An ordinary task that merely starts with
// "dream-" does not match.
var mintedDreamSlugRE = regexp.MustCompile(`^dream-[0-9]{8}-[0-9a-f]{4}(-[0-9]{1,2})?$`)

type Session struct {
	Project string
	Kind    Kind
	Slug    string
	Path    string
	First   time.Time
	Last    time.Time
}

type rawEntry struct {
	Type      string          `json:"type"`
	Cwd       string          `json:"cwd"`
	Timestamp string          `json:"timestamp"`
	Message   json.RawMessage `json:"message"`
}

// Discover walks projectsRoot for transcripts whose last activity is
// after since, and returns only spore-managed sessions. The second
// return names every transcript Discover could not fully classify, so a
// corpus with one broken file does not read the same as a quiet night:
// the broken file is reported alongside whatever the rest of the corpus
// yielded, rather than silently dropped.
func Discover(projectsRoot, home string, since time.Time) ([]Session, []TranscriptError, error) {
	entries, err := os.ReadDir(projectsRoot)
	if err != nil {
		return nil, nil, err
	}
	var out []Session
	var errs []TranscriptError
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		dir := filepath.Join(projectsRoot, e.Name())
		files, err := os.ReadDir(dir)
		if err != nil {
			errs = append(errs, TranscriptError{dir, err.Error()})
			continue
		}
		for _, f := range files {
			if f.IsDir() || !strings.HasSuffix(f.Name(), ".jsonl") {
				continue
			}
			path := filepath.Join(dir, f.Name())
			s, ok, err := classify(path, home)
			if err != nil {
				errs = append(errs, TranscriptError{path, err.Error()})
				continue
			}
			if !ok || !s.Last.After(since) {
				continue
			}
			out = append(out, s)
		}
	}
	return out, errs, nil
}

func classify(path, home string) (Session, bool, error) {
	f, err := os.Open(path)
	if err != nil {
		return Session{}, false, err
	}
	defer f.Close()

	s := Session{Path: path}
	var cwd, firstUser string
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	for sc.Scan() {
		var e rawEntry
		if json.Unmarshal(sc.Bytes(), &e) != nil {
			continue
		}
		if cwd == "" && e.Cwd != "" {
			cwd = e.Cwd
		}
		if ts, err := time.Parse(time.RFC3339, e.Timestamp); err == nil {
			if s.First.IsZero() || ts.Before(s.First) {
				s.First = ts
			}
			if ts.After(s.Last) {
				s.Last = ts
			}
		}
		if firstUser == "" && e.Type == "user" {
			firstUser = messageText(e.Message)
		}
	}
	// Scan stops silently on a line past the buffer's 8 MiB ceiling, so
	// without this check a transcript with one oversized line reads as a
	// session that legitimately ended early: First and Last only cover
	// whatever came before the line the scanner choked on.
	if err := sc.Err(); err != nil {
		return Session{}, false, err
	}
	if cwd == "" {
		return Session{}, false, nil
	}

	if project, slug, ok := workerPath(cwd, home); ok {
		// A dream worker's own session is new every night; digesting it
		// would mint a proposer on an otherwise quiet project forever.
		if mintedDreamSlugRE.MatchString(slug) {
			return Session{}, false, nil
		}
		s.Kind, s.Project, s.Slug = KindWorker, project, slug
		return s, true, nil
	}
	if strings.HasPrefix(strings.TrimSpace(firstUser), coordinatorMarker) {
		s.Kind, s.Project, s.Slug = KindCoordinator, filepath.Base(cwd), "coordinator"
		return s, true, nil
	}
	return Session{}, false, nil
}

// workerPath matches <home>/<project>/.worktrees/<slug> exactly. A
// deeper path (a subdirectory inside a worktree) is not a session root
// and is deliberately rejected.
func workerPath(cwd, home string) (project, slug string, ok bool) {
	rel, err := filepath.Rel(home, cwd)
	if err != nil {
		return "", "", false
	}
	parts := strings.Split(rel, string(filepath.Separator))
	if len(parts) != 3 || parts[1] != ".worktrees" {
		return "", "", false
	}
	if parts[0] == "" || parts[0] == ".." || parts[2] == "" {
		return "", "", false
	}
	return parts[0], parts[2], true
}

func messageText(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var m struct {
		Content any `json:"content"`
	}
	if json.Unmarshal(raw, &m) != nil {
		return ""
	}
	switch c := m.Content.(type) {
	case string:
		return c
	case []any:
		var b strings.Builder
		for _, part := range c {
			pm, ok := part.(map[string]any)
			if !ok {
				continue
			}
			if t, ok := pm["text"].(string); ok {
				b.WriteString(t)
				b.WriteString(" ")
			}
		}
		return b.String()
	}
	return ""
}
