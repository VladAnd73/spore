package sporetoml

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSetKey(t *testing.T) {
	cases := []struct {
		name, in, key, value, want string
	}{
		{
			name: "replace keeps comment and indent",
			in:   "# top\n[coordinator]\n  driver = \"claude\"   # who runs it\nmodel = 'opus'\n\n[fleet]\ndriver = \"x\"\n",
			key:  "driver", value: "codex",
			want: "# top\n[coordinator]\n  driver = \"codex\"   # who runs it\nmodel = 'opus'\n\n[fleet]\ndriver = \"x\"\n",
		},
		{
			name: "missing key goes after last entry",
			in:   "[coordinator]\ndriver = \"claude\"\n# trailing note\n\n[fleet]\nmax_workers = 3\n",
			key:  "model", value: "gpt-5.5",
			want: "[coordinator]\ndriver = \"claude\"\nmodel = \"gpt-5.5\"\n# trailing note\n\n[fleet]\nmax_workers = 3\n",
		},
		{
			name: "empty section gets key after header",
			in:   "[coordinator]\n\n[fleet]\nmax_workers = 3",
			key:  "driver", value: "codex",
			want: "[coordinator]\ndriver = \"codex\"\n\n[fleet]\nmax_workers = 3",
		},
		{
			name: "missing section is appended",
			in:   "[fleet]\nmax_workers = 3\n",
			key:  "driver", value: "codex",
			want: "[fleet]\nmax_workers = 3\n\n[coordinator]\ndriver = \"codex\"\n",
		},
		{
			name: "empty file",
			in:   "",
			key:  "driver", value: "codex",
			want: "[coordinator]\ndriver = \"codex\"\n",
		},
		{
			name: "subsection is not the section",
			in:   "[coordinator.x]\ndriver = \"a\"\n",
			key:  "driver", value: "codex",
			want: "[coordinator.x]\ndriver = \"a\"\n\n[coordinator]\ndriver = \"codex\"\n",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := SetKey(c.in, "coordinator", c.key, c.value); got != c.want {
				t.Errorf("got:\n%q\nwant:\n%q", got, c.want)
			}
		})
	}
}

func TestDeleteKey(t *testing.T) {
	in := "[coordinator]\ndriver = \"codex\"\nmodel = \"opus\" # pinned\nbrief = \"r.md\"\n[x]\nmodel = 1\n"
	want := "[coordinator]\ndriver = \"codex\"\nbrief = \"r.md\"\n[x]\nmodel = 1\n"
	if got := DeleteKey(in, "coordinator", "model"); got != want {
		t.Errorf("got:\n%q\nwant:\n%q", got, want)
	}
	if got := DeleteKey(want, "coordinator", "model"); got != want {
		t.Errorf("absent key changed content:\n%q", got)
	}
}

func TestWriteFileKeepsMode(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "spore.toml")
	if err := os.WriteFile(path, []byte("a = 1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := WriteFile(dir, "a = 2\n"); err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if st.Mode().Perm() != 0o600 {
		t.Errorf("mode = %v, want 0600", st.Mode().Perm())
	}
	if b, _ := os.ReadFile(path); string(b) != "a = 2\n" {
		t.Errorf("content = %q", b)
	}
}

func TestQuoteIsTOML(t *testing.T) {
	cases := map[string]string{
		"gpt-5.5":     `"gpt-5.5"`,
		`a"b\c`:       `"a\"b\\c"`,
		"tab\there\n": `"tab\there\n"`,
		"bell\a\x7f":  `"bell\u0007\u007F"`,
		"caf\u00e9":   "\"caf\u00e9\"",
	}
	for in, want := range cases {
		if got := Quote(in); got != want {
			t.Errorf("Quote(%q) = %s, want %s", in, got, want)
		}
	}
}

func TestTopLevelKeys(t *testing.T) {
	cases := []struct {
		name, in, want string
	}{
		{"empty file", "", "app_id = 7\n"},
		{"after last top-level entry", "# c\na = 1\n\n[s]\nb = 2\n", "# c\na = 1\napp_id = 7\n\n[s]\nb = 2\n"},
		{"replace", "app_id = 1 # old\n[s]\napp_id = 9\n", "app_id = 7 # old\n[s]\napp_id = 9\n"},
		{"only a section", "[s]\nb = 2\n", "app_id = 7\n[s]\nb = 2\n"},
	}
	for _, c := range cases {
		if got := SetKeyRaw(c.in, "", "app_id", "7"); got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}
	entries, err := ParseSection("a = \"x\"\n[s]\nb = 2\n", "")
	if err != nil || len(entries) != 1 || entries[0].Key != "a" || entries[0].Value != "x" {
		t.Fatalf("entries = %+v err = %v", entries, err)
	}
	if got := DeleteKey("a = 1\nb = 2\n[s]\na = 3\n", "", "a"); got != "b = 2\n[s]\na = 3\n" {
		t.Fatalf("delete = %q", got)
	}
}
