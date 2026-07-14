package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestValidateEditor(t *testing.T) {
	tests := []struct {
		name    string
		editor  string
		wantErr bool
		errMsg  string
	}{
		{
			name:    "vim is allowed",
			editor:  "vim",
			wantErr: false,
		},
		{
			name:    "vi is allowed",
			editor:  "vi",
			wantErr: false,
		},
		{
			name:    "nano is allowed",
			editor:  "nano",
			wantErr: false,
		},
		{
			name:    "emacs is allowed",
			editor:  "emacs",
			wantErr: false,
		},
		{
			name:    "code is allowed",
			editor:  "code",
			wantErr: false,
		},
		{
			name:    "nvim is allowed",
			editor:  "nvim",
			wantErr: false,
		},
		{
			name:    "unknown editor with relative path rejected",
			editor:  "myeditor",
			wantErr: true,
			errMsg:  "must be specified as absolute path",
		},
		{
			name:    "malicious command rejected",
			editor:  "rm -rf /",
			wantErr: true,
			errMsg:  "must be specified as absolute path",
		},
		{
			name:    "command with pipe rejected",
			editor:  "vim | sh",
			wantErr: true,
			errMsg:  "must be specified as absolute path",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateEditor(tt.editor)
			if (err != nil) != tt.wantErr {
				t.Errorf("validateEditor() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if tt.wantErr && !strings.Contains(err.Error(), tt.errMsg) {
				t.Errorf("validateEditor() error = %v, want error containing %q", err, tt.errMsg)
			}
		})
	}
}

func TestValidateEditorAbsolutePath(t *testing.T) {
	// Create a temporary executable
	tmpDir := t.TempDir()
	editorPath := filepath.Join(tmpDir, "test-editor")

	// Create executable file
	f, err := os.Create(editorPath)
	if err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}
	f.Close()

	if err := os.Chmod(editorPath, 0755); err != nil {
		t.Fatalf("Failed to chmod: %v", err)
	}

	t.Run("valid absolute path to existing editor", func(t *testing.T) {
		err := validateEditor(editorPath)
		if err != nil {
			t.Errorf("validateEditor() error = %v, want nil for valid absolute path", err)
		}
	})

	t.Run("absolute path to non-existent editor rejected", func(t *testing.T) {
		nonExistent := filepath.Join(tmpDir, "non-existent-editor")
		err := validateEditor(nonExistent)
		if err == nil {
			t.Error("validateEditor() should reject non-existent editor")
		}
		if !strings.Contains(err.Error(), "not found") {
			t.Errorf("validateEditor() error = %v, want error containing 'not found'", err)
		}
	})
}

func TestValidateEditorWithPath(t *testing.T) {
	// Test editor in PATH with directory prefix
	t.Run("editor with path prefix uses basename", func(t *testing.T) {
		err := validateEditor("/usr/bin/vim")
		// Should pass because basename is "vim" which is in allowed list
		if err != nil {
			t.Errorf("validateEditor() error = %v, want nil for /usr/bin/vim", err)
		}
	})
}

func TestValidateEditorCaseSensitive(t *testing.T) {
	// Verify case sensitivity
	t.Run("VIM (uppercase) rejected", func(t *testing.T) {
		err := validateEditor("VIM")
		if err == nil {
			t.Error("validateEditor() should reject uppercase VIM (case sensitive)")
		}
	})
}
