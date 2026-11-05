package config

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

const rewriteFixture = `# HRPAuth-Yggdrasil-API deployment config
version: "4"

server:
    port: ":2770"

microservice:
    name: HRPAuth-Yggdrasil-API
    ttl_seconds: 120

core_api:
    base_url: http://hrpauth:2778
    client_id: hrpauth-internal-super
    client_secret: super-secret
`

func writeFixture(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	return path
}

func TestRewriteClientCredentialsReplacesCoreAPIPair(t *testing.T) {
	path := writeFixture(t, rewriteFixture)

	n, err := RewriteClientCredentials(path, "hrpauth-internal-super", "super-secret", "HRPAuth-Yggdrasil-API", "low-secret", []string{"core_api"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if n != 1 {
		t.Fatalf("sections replaced = %d, want 1 (core_api)", n)
	}

	out, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read rewritten: %v", err)
	}
	text := string(out)
	if strings.Contains(text, "super-secret") {
		t.Error("rewritten config still contains the super secret")
	}
	if !strings.Contains(text, "client_id: HRPAuth-Yggdrasil-API") || !strings.Contains(text, "client_secret: low-secret") {
		t.Errorf("core_api section must carry the new pair, got:\n%s", text)
	}
	if !strings.Contains(text, "# HRPAuth-Yggdrasil-API deployment config") {
		t.Error("comments must survive the rewrite")
	}
	if !strings.Contains(text, "name: HRPAuth-Yggdrasil-API") || !strings.Contains(text, "base_url: http://hrpauth:2778") {
		t.Error("unrelated fields must survive the rewrite")
	}

	bak, err := os.ReadFile(path + ".bak")
	if err != nil {
		t.Fatalf("read backup: %v", err)
	}
	if string(bak) != rewriteFixture {
		t.Error(".bak must contain the pre-change content")
	}

	for _, p := range []string{path, path + ".bak"} {
		info, err := os.Stat(p)
		if err != nil {
			t.Fatalf("stat %s: %v", p, err)
		}
		if info.Mode().Perm() != 0600 {
			t.Errorf("%s mode = %v, want 0600", p, info.Mode().Perm())
		}
	}
}

func TestRewriteClientCredentialsNoMatchIsNoop(t *testing.T) {
	path := writeFixture(t, rewriteFixture)
	before, _ := os.ReadFile(path)

	n, err := RewriteClientCredentials(path, "nobody", "nothing", "x", "y", []string{"core_api"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if n != 0 {
		t.Fatalf("sections replaced = %d, want 0", n)
	}
	after, _ := os.ReadFile(path)
	if string(before) != string(after) {
		t.Error("no-match rewrite must not touch the file")
	}
	if _, err := os.Stat(path + ".bak"); !os.IsNotExist(err) {
		t.Error("no-match rewrite must not create a backup")
	}
}

func TestRewriteClientCredentialsLeavesDifferentPair(t *testing.T) {
	path := writeFixture(t, strings.Replace(rewriteFixture,
		"client_id: hrpauth-internal-super\n    client_secret: super-secret",
		"client_id: other-client\n    client_secret: other-secret", 1))

	n, err := RewriteClientCredentials(path, "hrpauth-internal-super", "super-secret", "x", "y", []string{"core_api"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if n != 0 {
		t.Fatalf("sections replaced = %d, want 0 (pair differs)", n)
	}
	out, _ := os.ReadFile(path)
	if !strings.Contains(string(out), "other-secret") {
		t.Error("a section with different credentials must stay untouched")
	}
}

func TestRewriteClientCredentialsRejectsEmptyValues(t *testing.T) {
	path := writeFixture(t, rewriteFixture)
	if _, err := RewriteClientCredentials(path, "", "", "a", "b", []string{"core_api"}); err == nil {
		t.Fatal("expected an error for empty old credentials")
	}
	if _, err := RewriteClientCredentials(path, "x", "y", "", "", []string{"core_api"}); err == nil {
		t.Fatal("expected an error for empty new credentials")
	}
}

func TestRewriteClientCredentialsInvalidSource(t *testing.T) {
	path := writeFixture(t, "not: [valid yaml")
	if _, err := RewriteClientCredentials(path, "a", "b", "c", "d", []string{"core_api"}); err == nil {
		t.Fatal("expected an error for an unparseable config file")
	}
}

func TestRewriteClientCredentialsMissingSection(t *testing.T) {
	path := writeFixture(t, "version: \"4\"\n")
	n, err := RewriteClientCredentials(path, "a", "b", "c", "d", []string{"core_api"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if n != 0 {
		t.Fatalf("sections replaced = %d, want 0 for a missing section", n)
	}
}

func TestRewriteClientCredentialsWriteFailureKeepsFile(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX permission bits only")
	}
	if os.Getuid() == 0 {
		t.Skip("root bypasses directory permission checks")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(rewriteFixture), 0644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	if err := os.Chmod(dir, 0500); err != nil {
		t.Fatalf("chmod dir: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0700) })

	if _, err := RewriteClientCredentials(path, "hrpauth-internal-super", "super-secret", "x", "y", []string{"core_api"}); err == nil {
		t.Fatal("expected an error when the directory is read-only")
	}
	out, _ := os.ReadFile(path)
	if string(out) != rewriteFixture {
		t.Error("a failed write-back must leave the original file intact")
	}
}

func TestIsRenderedDeployment(t *testing.T) {
	if IsRenderedDeployment() {
		t.Fatal("no render envs set, must report false")
	}
	t.Setenv(RenderedConfigTemplateEnv, "/etc/ha/config.yaml.tmpl")
	t.Setenv(RenderedConfigPathEnv, "/app/config.yaml")
	if !IsRenderedDeployment() {
		t.Fatal("both render envs set, must report true")
	}
}
