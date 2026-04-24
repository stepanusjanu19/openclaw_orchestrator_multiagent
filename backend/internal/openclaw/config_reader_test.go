package openclaw

import (
	"os"
	"path/filepath"
	"testing"
)

func writeTestConfig(t *testing.T, dir, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, "openclaw.json"), []byte(body), 0644); err != nil {
		t.Fatalf("write config: %v", err)
	}
}

func TestGetProviderModelsFiltersByProvider(t *testing.T) {
	dir := t.TempDir()
	writeTestConfig(t, dir, `{
  "agents": {
    "defaults": {
      "models": {
        "openai/gpt-4.1": {},
        "anthropic/claude-3-5-sonnet": {}
      }
    }
  },
  "models": {
    "providers": {
      "openai": {
        "models": [{ "id": "gpt-4.1-mini" }]
      },
      "anthropic": {
        "models": [{ "id": "claude-3-5-haiku" }]
      }
    }
  }
}`)

	models, err := GetProviderModels(dir, "openai")
	if err != nil {
		t.Fatalf("GetProviderModels: %v", err)
	}
	if len(models) == 0 {
		t.Fatalf("expected openai models, got none")
	}
	for _, model := range models {
		if model.Provider != "openai" {
			t.Fatalf("expected only openai models, got provider=%s", model.Provider)
		}
	}
}

func TestUpdateAgentModelInOCConfigUpdatesExistingEntry(t *testing.T) {
	dir := t.TempDir()
	writeTestConfig(t, dir, `{
  "agents": {
    "list": [
      { "id": "main", "model": "openai/gpt-4.1" }
    ]
  }
}`)

	if err := UpdateAgentModelInOCConfig(dir, "main", "anthropic/claude-3-5-sonnet"); err != nil {
		t.Fatalf("UpdateAgentModelInOCConfig: %v", err)
	}

	cfg, err := ReadOCConfig(dir)
	if err != nil {
		t.Fatalf("ReadOCConfig: %v", err)
	}
	if len(cfg.Agents.List) != 1 {
		t.Fatalf("expected 1 agent, got %d", len(cfg.Agents.List))
	}
	if cfg.Agents.List[0].Model.Primary != "anthropic/claude-3-5-sonnet" {
		t.Fatalf("unexpected model: %s", cfg.Agents.List[0].Model.Primary)
	}
}
