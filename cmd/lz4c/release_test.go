package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestReleaseNextVersion checks the version release.sh tags, from the lz4c
// tags and cmd/lz4c/BUMP, in a repository made for each case.
func TestReleaseNextVersion(t *testing.T) {
	for _, tool := range []string{"git", "sh"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("needs %s", tool)
		}
	}
	script, err := os.ReadFile("release.sh")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name    string
		tags    []string
		bump    string // contents of BUMP; none if empty
		want    string
		wantErr bool
	}{
		{name: "first release", want: "v1.0.0"},
		{name: "first release, minor", bump: "minor\n", want: "v1.0.0"},
		{name: "patch", tags: []string{"v1.0.0", "v1.0.1"}, want: "v1.0.2"},
		{name: "patch sorts numerically", tags: []string{"v1.0.9", "v1.0.10"}, want: "v1.0.11"},
		{name: "explicit patch", tags: []string{"v1.0.1"}, bump: "patch\n", want: "v1.0.2"},
		{name: "minor", tags: []string{"v1.0.0", "v1.0.1"}, bump: "minor\n", want: "v1.1.0"},
		{name: "minor with a comment", tags: []string{"v1.0.1"}, bump: "# new flags\nminor  # and Go 1.27\n", want: "v1.1.0"},
		{name: "minor after minor", tags: []string{"v1.1.3"}, bump: "minor", want: "v1.2.0"},
		{name: "pre-releases ignored", tags: []string{"v1.0.1", "v1.1.0-rc.1"}, want: "v1.0.2"},
		{name: "major refused", tags: []string{"v1.0.1"}, bump: "major\n", wantErr: true},
		{name: "empty refused", tags: []string{"v1.0.1"}, bump: "# nothing\n", wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			git := func(args ...string) {
				t.Helper()
				cmd := exec.Command("git", args...)
				cmd.Dir = dir
				cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1",
					"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com")
				if out, err := cmd.CombinedOutput(); err != nil {
					t.Fatalf("git %v: %v: %s", args, err, out)
				}
			}
			lz4c := filepath.Join(dir, "cmd", "lz4c")
			if err := os.MkdirAll(lz4c, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(lz4c, "release.sh"), script, 0o755); err != nil {
				t.Fatal(err)
			}
			if tc.bump != "" {
				if err := os.WriteFile(filepath.Join(lz4c, "BUMP"), []byte(tc.bump), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			git("init", "-q")
			git("add", ".")
			git("commit", "-q", "-m", "init")
			for _, tag := range tc.tags {
				git("tag", "cmd/lz4c/"+tag)
			}
			out, err := exec.Command("sh", filepath.Join(lz4c, "release.sh"), "-next").CombinedOutput()
			if tc.wantErr {
				if err == nil {
					t.Fatalf("got %q, want an error", out)
				}
				return
			}
			if err != nil {
				t.Fatalf("%v: %s", err, out)
			}
			if got := strings.TrimSpace(string(out)); got != tc.want {
				t.Fatalf("got %s, want %s", got, tc.want)
			}
		})
	}
}
