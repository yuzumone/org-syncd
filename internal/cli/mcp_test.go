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

func TestMCPCommandHasNoConfigurationFlags(t *testing.T) {
	cmd := newMCPCommand()
	for _, name := range []string{
		"transport", "listen", "http-path", "auth-token",
		"device-id", "couchdb-url", "database", "username", "password",
	} {
		if cmd.Flag(name) != nil {
			t.Errorf("unexpected MCP configuration flag --%s", name)
		}
	}
}

func TestNewMCPVaultRequiresCouchDBURL(t *testing.T) {
	t.Setenv("COUCHDB_URL", "")
	if _, err := newMCPVault(); err == nil {
		t.Fatal("expected missing COUCHDB_URL error")
	}
}

func TestNewMCPVaultReadsEnvironment(t *testing.T) {
	t.Setenv("COUCHDB_URL", "http://couchdb:5984")
	t.Setenv("COUCHDB_DATABASE", "orgsync-test")
	t.Setenv("COUCHDB_USER", "user")
	t.Setenv("COUCHDB_PASSWORD", "password")
	t.Setenv("DEVICE_ID", "mcp-test")
	if _, err := newMCPVault(); err != nil {
		t.Fatal(err)
	}
}

func TestNewMCPAuthReadsEnvironment(t *testing.T) {
	t.Setenv("BASE_URL", "http://localhost:8080")
	t.Setenv("MCP_AUTH_TOKEN", "secret")
	t.Setenv("DATA_DIR", t.TempDir())
	t.Setenv("MCP_REFRESH_DAYS", "7")
	if _, err := newMCPAuth("8080"); err != nil {
		t.Fatal(err)
	}
}

func TestNewMCPAuthRejectsInvalidRefreshDays(t *testing.T) {
	t.Setenv("MCP_REFRESH_DAYS", "invalid")
	if _, err := newMCPAuth("8080"); err == nil {
		t.Fatal("expected MCP_REFRESH_DAYS error")
	}
}
