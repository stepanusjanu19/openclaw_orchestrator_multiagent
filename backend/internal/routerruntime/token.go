package routerruntime

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

type runtimeDB struct {
	APIKeys []apiKeyEntry `json:"apiKeys"`
}

type apiKeyEntry struct {
	Key    string `json:"key"`
	Token  string `json:"token"`
	Value  string `json:"value"`
	APIKey string `json:"apiKey"`
}

func APIKey(tool, runtimeDir string) (string, bool) {
	runtimeDir = strings.TrimSpace(runtimeDir)
	if runtimeDir == "" {
		if dir, err := RuntimeDir(tool); err == nil {
			runtimeDir = dir
		}
	}
	if runtimeDir == "" {
		return "", false
	}
	for _, path := range dbCandidates(tool, runtimeDir) {
		token, ok := readAPIKey(path)
		if ok {
			return token, true
		}
	}
	return "", false
}

func RuntimeDir(tool string) (string, error) {
	base, err := os.UserCacheDir()
	if err != nil || strings.TrimSpace(base) == "" {
		base = filepath.Join(os.TempDir(), "openclaw-control-cache")
	}
	return filepath.Join(base, "openclaw-control", "router-runtime", sanitizeToolName(tool)), nil
}

func DisplayName(tool string) string {
	switch tool {
	case "9router":
		return "9Router"
	case "omniroute":
		return "OmniRoute"
	default:
		return "router " + tool
	}
}

func dbCandidates(tool, runtimeDir string) []string {
	names := []string{"." + sanitizeToolName(tool)}
	switch tool {
	case "9router":
		names = append(names, ".9router")
	case "omniroute":
		names = append(names, ".omniroute")
	}
	seen := map[string]bool{}
	paths := []string{}
	for _, name := range names {
		path := filepath.Join(runtimeDir, "home", name, "db.json")
		if !seen[path] {
			paths = append(paths, path)
			seen[path] = true
		}
	}
	for _, path := range []string{
		filepath.Join(runtimeDir, "home", "db.json"),
		filepath.Join(runtimeDir, "data", "db.json"),
	} {
		if !seen[path] {
			paths = append(paths, path)
			seen[path] = true
		}
	}
	return paths
}

func readAPIKey(path string) (string, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", false
	}
	var db runtimeDB
	if err := json.Unmarshal(data, &db); err != nil {
		return "", false
	}
	for _, entry := range db.APIKeys {
		for _, token := range []string{entry.Key, entry.Token, entry.Value, entry.APIKey} {
			token = strings.TrimSpace(token)
			if token != "" {
				return token, true
			}
		}
	}
	return "", false
}

func sanitizeToolName(tool string) string {
	tool = strings.ToLower(strings.TrimSpace(tool))
	var b strings.Builder
	for _, r := range tool {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			b.WriteRune(r)
		}
	}
	if b.Len() == 0 {
		return "router"
	}
	return b.String()
}
