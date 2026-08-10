package llm

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadConfigFileAndEnvironmentOverride(t *testing.T) {
	path := filepath.Join(t.TempDir(), "llm.json")
	data := []byte(`{"base_url":"https://api.deepseek.com","api_key":"file-secret","model":"deepseek-v4-pro"}`)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CAPSULE_LLM_CONFIG_FILE", path)
	t.Setenv("CAPSULE_LLM_BASE_URL", "")
	t.Setenv("CAPSULE_LLM_API_KEY", "environment-secret")
	t.Setenv("CAPSULE_LLM_MODEL", "")
	config, err := LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	absolute, _ := filepath.Abs(path)
	if config.BaseURL != "https://api.deepseek.com" || config.APIKey != "environment-secret" ||
		config.Model != "deepseek-v4-pro" || config.Thinking != "disabled" || config.SourceFile != absolute {
		t.Fatalf("unexpected merged config: endpoint=%s model=%s source=%s", config.BaseURL, config.Model, config.SourceFile)
	}
}

func TestLoadConfigFileSecurityAndStrictJSON(t *testing.T) {
	tests := []struct {
		name string
		data string
		mode os.FileMode
		want string
	}{
		{name: "permissions", data: `{}`, mode: 0o644, want: "0600"},
		{name: "unknown field", data: `{"api_key":"secret","unknown":true}`, mode: 0o600, want: "unknown field"},
		{name: "trailing JSON", data: `{} {}`, mode: 0o600, want: "trailing JSON"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "llm.json")
			if err := os.WriteFile(path, []byte(test.data), test.mode); err != nil {
				t.Fatal(err)
			}
			if err := os.Chmod(path, test.mode); err != nil {
				t.Fatal(err)
			}
			t.Setenv("CAPSULE_LLM_CONFIG_FILE", path)
			if _, err := LoadConfig(); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("expected %q error, got %v", test.want, err)
			}
		})
	}
}

func TestLoadConfigRejectsSymlink(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "target.json")
	link := filepath.Join(root, "link.json")
	if err := os.WriteFile(target, []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CAPSULE_LLM_CONFIG_FILE", link)
	if _, err := LoadConfig(); err == nil || !strings.Contains(err.Error(), "non-symlink") {
		t.Fatalf("expected symlink rejection, got %v", err)
	}
}
