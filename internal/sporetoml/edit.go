package sporetoml

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// SetKey returns content with `key = "value"` in [section]. An existing
// line keeps its indentation and trailing comment; a missing key goes
// after the section's last entry; a missing section is appended. Other
// lines pass through byte for byte. Section "" is the top-level table:
// a missing key goes after its last entry, else at the top.
func SetKey(content, section, key, value string) string {
	return SetKeyRaw(content, section, key, Quote(value))
}

// SetKeyRaw is SetKey with raw written verbatim as the value, for
// integers and booleans.
func SetKeyRaw(content, section, key, raw string) string {
	lines, trailingNL := splitLines(content)
	idx, last, header := locate(lines, section, key)
	entry := key + " = " + raw
	switch {
	case idx >= 0:
		lines[idx] = rewriteEntry(lines[idx], entry)
	case section == "":
		at := last + 1
		lines = append(lines[:at], append([]string{entry}, lines[at:]...)...)
		if len(lines) == 1 {
			trailingNL = true
		}
	case header >= 0:
		at := header
		if last >= 0 {
			at = last
		}
		lines = append(lines[:at+1], append([]string{entry}, lines[at+1:]...)...)
	default:
		if len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) != "" {
			lines = append(lines, "")
		}
		lines = append(lines, "["+section+"]", entry)
		trailingNL = true
	}
	return joinLines(lines, trailingNL)
}

// DeleteKey returns content without key's line in [section]. A missing
// key returns content unchanged.
func DeleteKey(content, section, key string) string {
	lines, trailingNL := splitLines(content)
	idx, _, _ := locate(lines, section, key)
	if idx < 0 {
		return content
	}
	lines = append(lines[:idx], lines[idx+1:]...)
	return joinLines(lines, trailingNL)
}

// WriteFile replaces <projectRoot>/spore.toml via a temp file and
// rename, keeping the old file's mode (0644 for a new file).
func WriteFile(projectRoot, content string) error {
	path := filepath.Join(projectRoot, "spore.toml")
	mode := os.FileMode(0o644)
	if st, err := os.Stat(path); err == nil {
		mode = st.Mode().Perm()
	}
	tmp, err := os.CreateTemp(projectRoot, ".spore.toml.*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.WriteString(content); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmp.Name(), mode); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// Quote renders v as a TOML basic string. strconv.Quote is not one:
// its \x and \a escapes are invalid TOML. Non-ASCII passes through
// (TOML files are UTF-8); control bytes get TOML's short or \uXXXX
// escapes.
func Quote(v string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range v {
		switch r {
		case '"':
			b.WriteString(`\"`)
		case '\\':
			b.WriteString(`\\`)
		case '\b':
			b.WriteString(`\b`)
		case '\t':
			b.WriteString(`\t`)
		case '\n':
			b.WriteString(`\n`)
		case '\f':
			b.WriteString(`\f`)
		case '\r':
			b.WriteString(`\r`)
		default:
			if r < 0x20 || r == 0x7f {
				fmt.Fprintf(&b, `\u%04X`, r)
			} else {
				b.WriteRune(r)
			}
		}
	}
	b.WriteByte('"')
	return b.String()
}

// locate returns the index of key's line in [section], the section's
// last entry line, and its header line; -1 for each one absent. The
// first [section] header wins.
func locate(lines []string, section, key string) (idx, last, header int) {
	idx, last, header = -1, -1, -1
	in := section == ""
	for i, raw := range lines {
		line := strings.TrimSpace(StripComment(raw))
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			if in {
				return
			}
			in = strings.TrimSpace(line[1:len(line)-1]) == section
			if in {
				header = i
			}
			continue
		}
		if !in {
			continue
		}
		last = i
		if eq := strings.IndexByte(line, '='); eq > 0 && strings.TrimSpace(line[:eq]) == key && idx < 0 {
			idx = i
		}
	}
	return
}

func rewriteEntry(raw, entry string) string {
	body := StripComment(raw)
	comment := raw[len(body):]
	indent := raw[:len(raw)-len(strings.TrimLeft(raw, " \t"))]
	if comment == "" {
		return indent + entry
	}
	gap := body[len(strings.TrimRight(body, " \t")):]
	if gap == "" {
		gap = " "
	}
	return indent + entry + gap + comment
}

func splitLines(content string) ([]string, bool) {
	if content == "" {
		return nil, false
	}
	trailingNL := strings.HasSuffix(content, "\n")
	return strings.Split(strings.TrimSuffix(content, "\n"), "\n"), trailingNL
}

func joinLines(lines []string, trailingNL bool) string {
	out := strings.Join(lines, "\n")
	if trailingNL && len(lines) > 0 {
		out += "\n"
	}
	return out
}
