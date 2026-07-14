package context

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestValidateDatabaseBasePath(t *testing.T) {
	tests := []struct {
		name    string
		path    string
		wantErr bool
		errMsg  string
	}{
		{
			name:    "valid absolute path",
			path:    "/tmp/summoner-test",
			wantErr: false,
		},
		{
			name:    "relative path rejected",
			path:    "relative/path",
			wantErr: true,
			errMsg:  "path must be absolute",
		},
		{
			name:    "path with traversal rejected",
			path:    "/tmp/../etc/summoner",
			wantErr: true,
			errMsg:  "path contains traversal elements",
		},
		{
			name:    "path with double dot rejected",
			path:    "/tmp/summoner/../data",
			wantErr: true,
			errMsg:  "path contains traversal elements",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateDatabaseBasePath(tt.path)
			if (err != nil) != tt.wantErr {
				t.Errorf("validateDatabaseBasePath() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if tt.wantErr && !strings.Contains(err.Error(), tt.errMsg) {
				t.Errorf("validateDatabaseBasePath() error = %v, want error containing %q", err, tt.errMsg)
			}
		})
	}
}

func TestGetDatabasePath(t *testing.T) {
	// Save original env
	origPath := os.Getenv("SUMMONER_DB_PATH")
	defer os.Setenv("SUMMONER_DB_PATH", origPath)

	t.Run("uses default path when env not set", func(t *testing.T) {
		os.Unsetenv("SUMMONER_DB_PATH")
		path := getDatabasePath("test123")

		if !strings.Contains(path, ".claude/plugins/summoner/memory") {
			t.Errorf("getDatabasePath() = %v, want path containing .claude/plugins/summoner/memory", path)
		}
		if !strings.HasSuffix(path, "test123.db") {
			t.Errorf("getDatabasePath() = %v, want path ending with test123.db", path)
		}
	})

	t.Run("uses env path when valid", func(t *testing.T) {
		tmpDir := t.TempDir()
		os.Setenv("SUMMONER_DB_PATH", tmpDir)

		path := getDatabasePath("test456")
		expected := filepath.Join(tmpDir, "test456.db")

		if path != expected {
			t.Errorf("getDatabasePath() = %v, want %v", path, expected)
		}
	})

	t.Run("falls back to default when env path invalid", func(t *testing.T) {
		os.Setenv("SUMMONER_DB_PATH", "relative/path")

		path := getDatabasePath("test789")

		if !strings.Contains(path, ".claude/plugins/summoner/memory") {
			t.Errorf("getDatabasePath() should fallback to default, got %v", path)
		}
	})
}

func TestGetDefaultDatabasePath(t *testing.T) {
	path := getDefaultDatabasePath("abc123")

	if !filepath.IsAbs(path) {
		t.Errorf("getDefaultDatabasePath() = %v, want absolute path", path)
	}

	if !strings.Contains(path, "abc123.db") {
		t.Errorf("getDefaultDatabasePath() = %v, want path containing abc123.db", path)
	}

	if !strings.Contains(path, ".claude") {
		t.Errorf("getDefaultDatabasePath() = %v, want path containing .claude", path)
	}
}

func TestHashProjectName(t *testing.T) {
	tests := []struct {
		name        string
		projectName string
		wantLen     int
	}{
		{
			name:        "simple project name",
			projectName: "my-project",
			wantLen:     16, // 8 bytes = 16 hex chars
		},
		{
			name:        "project with path",
			projectName: "/Users/admin/my-project",
			wantLen:     16,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			hash := hashProjectName(tt.projectName)

			if len(hash) != tt.wantLen {
				t.Errorf("hashProjectName() returned %d chars, want %d", len(hash), tt.wantLen)
			}

			// Verify it's hex
			for _, c := range hash {
				if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
					t.Errorf("hashProjectName() = %v, contains non-hex character %c", hash, c)
					break
				}
			}
		})
	}
}

func TestHashProjectNameDeterministic(t *testing.T) {
	projectName := "test-project"

	hash1 := hashProjectName(projectName)
	hash2 := hashProjectName(projectName)

	if hash1 != hash2 {
		t.Errorf("hashProjectName() not deterministic: %v != %v", hash1, hash2)
	}
}

func TestHashProjectNameUnique(t *testing.T) {
	hash1 := hashProjectName("project-one")
	hash2 := hashProjectName("project-two")

	if hash1 == hash2 {
		t.Errorf("hashProjectName() should produce different hashes for different projects")
	}
}

func TestNewMemory(t *testing.T) {
	t.Run("rejects empty project name", func(t *testing.T) {
		_, err := NewMemory("")
		if err == nil {
			t.Error("NewMemory() should reject empty project name")
		}
		if !strings.Contains(err.Error(), "empty") {
			t.Errorf("NewMemory() error = %v, want error containing 'empty'", err)
		}
	})

	t.Run("accepts valid project name", func(t *testing.T) {
		m, err := NewMemory("valid-project")
		if err != nil {
			t.Errorf("NewMemory() error = %v, want nil", err)
		}
		if m == nil {
			t.Error("NewMemory() returned nil memory")
		}
		if m != nil && m.db == nil {
			t.Error("NewMemory() returned memory with nil database")
		}

		// Cleanup
		if m != nil {
			m.Close()
		}
	})
}

func TestValidateProjectName(t *testing.T) {
	tests := []struct {
		name        string
		projectName string
		wantErr     bool
		errMsg      string
	}{
		{
			name:        "valid simple name",
			projectName: "my-project",
			wantErr:     false,
		},
		{
			name:        "valid with underscores",
			projectName: "my_project_123",
			wantErr:     false,
		},
		{
			name:        "empty name rejected",
			projectName: "",
			wantErr:     true,
			errMsg:      "empty",
		},
		{
			name:        "path separator rejected",
			projectName: "my/project",
			wantErr:     true,
			errMsg:      "invalid character",
		},
		{
			name:        "backslash rejected",
			projectName: "my\\project",
			wantErr:     true,
			errMsg:      "invalid character",
		},
		{
			name:        "dot dot rejected",
			projectName: "../project",
			wantErr:     true,
			errMsg:      "invalid character",
		},
		{
			name:        "null byte rejected",
			projectName: "project\x00name",
			wantErr:     true,
			errMsg:      "invalid character",
		},
		{
			name:        "newline rejected",
			projectName: "project\nname",
			wantErr:     true,
			errMsg:      "invalid character",
		},
		{
			name:        "too long rejected",
			projectName: strings.Repeat("a", 256),
			wantErr:     true,
			errMsg:      "too long",
		},
		{
			name:        "leading whitespace rejected",
			projectName: " project",
			wantErr:     true,
			errMsg:      "whitespace",
		},
		{
			name:        "trailing whitespace rejected",
			projectName: "project ",
			wantErr:     true,
			errMsg:      "whitespace",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateProjectName(tt.projectName)
			if (err != nil) != tt.wantErr {
				t.Errorf("validateProjectName() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if tt.wantErr && !strings.Contains(err.Error(), tt.errMsg) {
				t.Errorf("validateProjectName() error = %v, want error containing %q", err, tt.errMsg)
			}
		})
	}
}
