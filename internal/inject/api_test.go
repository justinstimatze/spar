package inject

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// isolateAPIKeySources clears the env var and points HOME at a fresh,
// empty directory so loadAPIKey's global ~/.config/spar/.env fallback
// can't accidentally pick up this machine's real key.
func isolateAPIKeySources(t *testing.T) {
	t.Helper()
	t.Setenv("ANTHROPIC_API_KEY", "")
	t.Setenv("HOME", t.TempDir())
}

func TestLoadAPIKeyFromEnv(t *testing.T) {
	isolateAPIKeySources(t)
	t.Setenv("ANTHROPIC_API_KEY", "sk-test-env")
	if got := loadAPIKey(); got != "sk-test-env" {
		t.Errorf("loadAPIKey() = %q, want sk-test-env", got)
	}
}

func TestLoadAPIKeyFromEnvStripsQuotes(t *testing.T) {
	isolateAPIKeySources(t)
	t.Setenv("ANTHROPIC_API_KEY", `"sk-test-env"`)
	if got := loadAPIKey(); got != "sk-test-env" {
		t.Errorf("loadAPIKey() = %q, want the quotes stripped", got)
	}
}

func TestLoadAPIKeyDotEnvWalkUp(t *testing.T) {
	isolateAPIKeySources(t)
	root := t.TempDir()
	sub := filepath.Join(root, "a", "b", "c")
	if err := os.MkdirAll(sub, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".env"), []byte("ANTHROPIC_API_KEY=sk-walked-up\n"), 0644); err != nil {
		t.Fatal(err)
	}
	t.Chdir(sub)
	if got := loadAPIKey(); got != "sk-walked-up" {
		t.Errorf("loadAPIKey() = %q, want sk-walked-up (found by walking up from %s)", got, sub)
	}
}

func TestLoadAPIKeyGlobalConfigFallback(t *testing.T) {
	isolateAPIKeySources(t)
	home := os.Getenv("HOME")
	cfgDir := filepath.Join(home, ".config", "spar")
	if err := os.MkdirAll(cfgDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cfgDir, ".env"), []byte("ANTHROPIC_API_KEY=sk-global\n"), 0644); err != nil {
		t.Fatal(err)
	}
	t.Chdir(t.TempDir()) // nowhere under this has its own .env
	if got := loadAPIKey(); got != "sk-global" {
		t.Errorf("loadAPIKey() = %q, want sk-global from the global config fallback", got)
	}
}

func TestLoadAPIKeyNoneFound(t *testing.T) {
	isolateAPIKeySources(t)
	t.Chdir(t.TempDir())
	if got := loadAPIKey(); got != "" {
		t.Errorf("loadAPIKey() = %q, want empty with no key anywhere", got)
	}
}

func TestResolveAPIKeySourceEnv(t *testing.T) {
	isolateAPIKeySources(t)
	t.Setenv("ANTHROPIC_API_KEY", "sk-test-env")
	key, source := ResolveAPIKey()
	if key != "sk-test-env" {
		t.Errorf("key = %q, want sk-test-env", key)
	}
	if source != "the ANTHROPIC_API_KEY environment variable" {
		t.Errorf("source = %q, want the environment-variable label", source)
	}
}

func TestResolveAPIKeySourceDotEnv(t *testing.T) {
	isolateAPIKeySources(t)
	dir := t.TempDir()
	envPath := filepath.Join(dir, ".env")
	if err := os.WriteFile(envPath, []byte("ANTHROPIC_API_KEY=sk-local\n"), 0644); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)
	key, source := ResolveAPIKey()
	if key != "sk-local" {
		t.Errorf("key = %q, want sk-local", key)
	}
	if source != envPath {
		t.Errorf("source = %q, want the .env path %q", source, envPath)
	}
}

func TestResolveAPIKeyNoneFound(t *testing.T) {
	isolateAPIKeySources(t)
	t.Chdir(t.TempDir())
	key, source := ResolveAPIKey()
	if key != "" || source != "" {
		t.Errorf("got key=%q source=%q, want both empty with no key anywhere", key, source)
	}
}

func TestReadEnvFromMissingFile(t *testing.T) {
	if got := readEnvFrom(filepath.Join(t.TempDir(), "does-not-exist", ".env")); got != "" {
		t.Errorf("readEnvFrom(missing) = %q, want empty", got)
	}
}

func TestReadEnvFromSkipsCommentsAndBlankLines(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".env")
	content := "# a comment\n\n# SPAR_INJECT_RATE=0.4\nANTHROPIC_API_KEY=sk-from-file\n"
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	if got := readEnvFrom(path); got != "sk-from-file" {
		t.Errorf("readEnvFrom() = %q, want sk-from-file", got)
	}
}

func TestStripQuotes(t *testing.T) {
	cases := map[string]string{
		`"quoted"`: "quoted",
		`'quoted'`: "quoted",
		"bare":     "bare",
		`"`:        `"`, // too short to be a real quoted pair
		"":         "",
	}
	for in, want := range cases {
		if got := stripQuotes(in); got != want {
			t.Errorf("stripQuotes(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestTruncate(t *testing.T) {
	if got := truncate("short", 300); got != "short" {
		t.Errorf("truncate(short) = %q, want unchanged", got)
	}
	long := strings.Repeat("x", 400)
	got := truncate(long, 300)
	if len([]rune(got)) != 301 { // 300 chars + the ellipsis rune
		t.Errorf("truncate(long, 300) length = %d, want 301", len([]rune(got)))
	}
	if !strings.HasSuffix(got, "…") {
		t.Errorf("truncate(long) = %q, want it to end with an ellipsis", got)
	}
	if got := truncate("line1\nline2", 300); strings.Contains(got, "\n") {
		t.Errorf("truncate() should collapse newlines, got %q", got)
	}
}

func TestSystemPromptForMatchesKind(t *testing.T) {
	code := systemPromptFor("code")
	prose := systemPromptFor("prose")

	if !strings.Contains(code, "off-by-one") || strings.Contains(code, "flipped-recommendation") {
		t.Error("code prompt should carry the code examples, not the prose ones")
	}
	if !strings.Contains(prose, "flipped-recommendation") || strings.Contains(prose, "off-by-one") {
		t.Error("prose prompt should carry the prose examples, not the code ones")
	}
	if !strings.Contains(code, "fail to compile") {
		t.Error("code prompt should keep the compile/syntax constraint")
	}
	if strings.Contains(prose, "fail to compile") {
		t.Error("prose prompt shouldn't carry the code-only compile/syntax constraint")
	}
	if code == prose {
		t.Error("code and prose prompts should differ")
	}
}

func TestBuildUserContent(t *testing.T) {
	got := buildUserContent("pkg/f.go", "line one\nline two", "lines 1-2", "off-by-one")
	for _, want := range []string{
		"File: pkg/f.go",
		"Category to use: off-by-one",
		"lines 1-2",
		"    1| line one",
		"    2| line two",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("buildUserContent output missing %q, got:\n%s", want, got)
		}
	}
}
