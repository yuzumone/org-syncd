package cli

import "testing"

func TestRootCommandHasNoConfigurationFlags(t *testing.T) {
	cmd := newRootCommand()
	for _, name := range []string{"config", "dry-run"} {
		if cmd.Flag(name) != nil {
			t.Errorf("unexpected configuration flag --%s", name)
		}
	}
}

func TestServeCommandHasNoConfigurationFlags(t *testing.T) {
	cmd := newServeCommand()
	for _, name := range []string{
		"transport", "listen", "http-path", "auth-token",
		"device-id", "couchdb-url", "database", "username", "password",
	} {
		if cmd.Flag(name) != nil {
			t.Errorf("unexpected serve configuration flag --%s", name)
		}
	}
}

func TestRootCommandUsesServeCommand(t *testing.T) {
	cmd := newRootCommand()
	if _, _, err := cmd.Find([]string{"serve"}); err != nil {
		t.Fatal("expected serve command")
	}
	if found, _, err := cmd.Find([]string{"mcp"}); err == nil && found.Name() == "mcp" {
		t.Fatal("unexpected mcp command")
	}
}

func TestNewServeVaultRequiresCouchDBURL(t *testing.T) {
	t.Setenv("COUCHDB_URL", "")
	if _, err := newServeVault(); err == nil {
		t.Fatal("expected missing COUCHDB_URL error")
	}
}

func TestNewServeVaultReadsEnvironment(t *testing.T) {
	t.Setenv("COUCHDB_URL", "http://couchdb:5984")
	t.Setenv("COUCHDB_DATABASE", "orgsync-test")
	t.Setenv("COUCHDB_USER", "user")
	t.Setenv("COUCHDB_PASSWORD", "password")
	t.Setenv("DEVICE_ID", "mcp-test")
	if _, err := newServeVault(); err != nil {
		t.Fatal(err)
	}
}

func TestNewServeAuthReadsEnvironment(t *testing.T) {
	t.Setenv("BASE_URL", "http://localhost:8080")
	t.Setenv("MCP_AUTH_TOKEN", "secret")
	t.Setenv("DATA_DIR", t.TempDir())
	t.Setenv("MCP_REFRESH_DAYS", "7")
	if _, err := newServeAuth("8080"); err != nil {
		t.Fatal(err)
	}
}

func TestNewServeAuthRejectsInvalidRefreshDays(t *testing.T) {
	t.Setenv("MCP_REFRESH_DAYS", "invalid")
	if _, err := newServeAuth("8080"); err == nil {
		t.Fatal("expected MCP_REFRESH_DAYS error")
	}
}
