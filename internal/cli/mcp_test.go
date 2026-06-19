package cli

import "testing"

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
