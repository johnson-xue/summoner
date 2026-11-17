package shared

import (
	"os"
	"path/filepath"
	"testing"
)

// shared_test.go — task-2.5 m4: ProjectKey must normalize the project
// directory (absolute path + symlink resolution) before hashing, so a session
// opened through a symlinked path (e.g. /var vs /private/var on macOS, or a
// user-level link to the repo) resolves to the same project key as the real
// path — two spellings of one checkout must not fork the state/trace trees.

// setProjectDir points CLAUDE_PROJECT_DIR at dir for the duration of the test
// (go1.16 has no t.Setenv).
func setProjectDir(t *testing.T, dir string) {
	t.Helper()
	old, had := os.LookupEnv("CLAUDE_PROJECT_DIR")
	if dir == "" {
		if had {
			if err := os.Setenv("CLAUDE_PROJECT_DIR", old); err != nil {
				t.Fatal(err)
			}
		} else if err := os.Unsetenv("CLAUDE_PROJECT_DIR"); err != nil {
			t.Fatal(err)
		}
		return
	}
	if err := os.Setenv("CLAUDE_PROJECT_DIR", dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { setProjectDir(t, "") })
}

func TestProjectKeyNormalizesSymlinksAndRelativePaths(t *testing.T) {
	real := t.TempDir()

	// A symlink to the real directory hashes to the same key.
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(real, link); err != nil {
		t.Skipf("symlink: %v", err)
	}
	setProjectDir(t, link)
	viaLink := ProjectKey()
	setProjectDir(t, real)
	viaReal := ProjectKey()
	if viaLink != viaReal {
		t.Fatalf("ProjectKey through symlink %q = %s, want %s (the real path's key)", link, viaLink, viaReal)
	}

	// A relative spelling of the same directory hashes to the same key.
	wd, err := os.Getwd()
	if err != nil {
		t.Skipf("getwd: %v", err)
	}
	setProjectDir(t, wd)
	viaAbs := ProjectKey()
	setProjectDir(t, ".")
	viaRel := ProjectKey()
	if viaAbs != viaRel {
		t.Fatalf("ProjectKey of %q = %s, want %s (the absolute spelling's key)", ".", viaRel, viaAbs)
	}
	setProjectDir(t, "")
}
