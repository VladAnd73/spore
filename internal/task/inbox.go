package task

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// ErrUnknownSlug reports a tell aimed at a destination nothing watches.
// Without it TellDir's MkdirAll would mint the directory on the spot,
// so a mistyped slug looked exactly like a successful delivery.
var ErrUnknownSlug = errors.New("unknown slug")

// reservedSlugs are destinations that legitimately have no tasks/<slug>.md.
// "coordinator" is the busiest destination in the fleet (every watcher
// escalation goes there), so a gate that forgot it would silence alerts on
// every project rather than only catching typos.
var reservedSlugs = map[string]bool{"coordinator": true}

// Tell appends a JSON message envelope to the per-slug inbox dir under
// StateDir, or to the coordinator inbox (CoordinatorInboxDirForProject)
// for slug "coordinator", which is not a per-project task slug. Filename is "<unix-nanos>.json", with a numeric suffix on
// collision; payload schema is {"slug","ts","msg"}. No agent wake
// side effect: the Stop-hook drain that turns inbox writes into
// resumed turns is downstream consumer territory.
//
// An unknown slug returns an error wrapping ErrUnknownSlug and writes
// nothing.
func Tell(slug, msg string) error {
	dir, err := tellInboxDir(slug)
	if err != nil {
		return err
	}
	// MainRepoRoot, not the cwd: a worker calling from its own worktree
	// sees no tasks/ there, and a cwd-relative lookup would reject every
	// slug in the project.
	root, err := MainRepoRoot("")
	if err != nil {
		return err
	}
	if err := checkDeliverable(filepath.Join(root, "tasks"), dir, slug); err != nil {
		return err
	}
	return TellDir(dir, slug, msg)
}

// checkDeliverable reports whether slug names something that will be read.
// Three ways to qualify, any one is enough:
//
//   - a reserved name, which never has a task file;
//   - an inbox directory that already exists, so somebody is draining it;
//   - a tasks/<slug>.md in tasksDir, in ANY status. Existence is the test,
//     not activity: queuing for a paused or blocked task is legitimate, and
//     status-gating would add a second way for delivery to fail.
func checkDeliverable(tasksDir, inboxDir, slug string) error {
	if slug == "" {
		return fmt.Errorf("%w: empty slug", ErrUnknownSlug)
	}
	if reservedSlugs[slug] {
		return nil
	}
	if fi, err := os.Stat(inboxDir); err == nil && fi.IsDir() {
		return nil
	}
	if _, err := os.Stat(filepath.Join(tasksDir, slug+".md")); err == nil {
		return nil
	}
	return fmt.Errorf("%w: %q has no %s and no inbox; nothing would read this message",
		ErrUnknownSlug, slug, filepath.Join(tasksDir, slug+".md"))
}

// TellDir is Tell against an explicit inbox dir, for callers that
// read the inbox from a session env rather than the cwd project.
func TellDir(dir, slug, msg string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	now := time.Now()
	payload := struct {
		Slug string `json:"slug"`
		TS   string `json:"ts"`
		Msg  string `json:"msg"`
	}{Slug: slug, TS: now.UTC().Format(time.RFC3339), Msg: msg}
	b, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	return writeUniqueInboxFile(dir, now.UnixNano(), b)
}

func tellInboxDir(slug string) (string, error) {
	if slug == "coordinator" {
		return CoordinatorInboxDirForProject("")
	}
	return InboxDir(slug)
}

func writeUniqueInboxFile(dir string, stamp int64, body []byte) error {
	for n := 0; n < 1000; n++ {
		name := fmt.Sprintf("%d.json", stamp)
		if n > 0 {
			name = fmt.Sprintf("%d-%d.json", stamp, n)
		}
		path := filepath.Join(dir, name)
		f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
		if os.IsExist(err) {
			continue
		}
		if err != nil {
			return err
		}
		if _, err := f.Write(body); err != nil {
			_ = f.Close()
			return err
		}
		return f.Close()
	}
	return fmt.Errorf("inbox: no free message filename for stamp %d", stamp)
}
