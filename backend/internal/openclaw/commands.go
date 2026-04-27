package openclaw

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"sort"
	"strings"
	"time"
)

const (
	CommandSafetyReadOnly = "read_only"
	CommandSafetyMutating = "mutating_confirm"
	CommandSafetyBlocked  = "blocked"
)

type CommandArg struct {
	Name        string   `json:"name"`
	Label       string   `json:"label"`
	Required    bool     `json:"required"`
	Placeholder string   `json:"placeholder,omitempty"`
	Options     []string `json:"options,omitempty"`
}

type CommandDefinition struct {
	ID          string       `json:"id"`
	Path        []string     `json:"path"`
	Category    string       `json:"category"`
	Safety      string       `json:"safety"`
	Runnable    bool         `json:"runnable"`
	Description string       `json:"description"`
	DocsURL     string       `json:"docs_url"`
	Args        []CommandArg `json:"args,omitempty"`
}

type CommandRunRequest struct {
	ID      string            `json:"id"`
	Args    map[string]string `json:"args"`
	Confirm bool              `json:"confirm"`
}

type CommandRunResult struct {
	ID                   string   `json:"id"`
	Argv                 []string `json:"argv,omitempty"`
	Stdout               string   `json:"stdout,omitempty"`
	Stderr               string   `json:"stderr,omitempty"`
	ExitCode             int      `json:"exit_code"`
	ConfirmationRequired bool     `json:"confirmation_required,omitempty"`
	Error                string   `json:"error,omitempty"`
}

type commandSpec struct {
	CommandDefinition
	build func(map[string]string) ([]string, error)
}

var safeToken = regexp.MustCompile(`^[A-Za-z0-9._:@/+=,-]{1,200}$`)
var sensitiveConfigKey = regexp.MustCompile(`(?i)(token|api[_-]?key|apikey|password|passwd|secret|credential|authorization|auth[_-]?header|private[_-]?key|env|database[_-]?url|pgvector|dsn)`)

var commandKeyValueSecretPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)\b([A-Z0-9_]*(?:TOKEN|SECRET|PASSWORD|API_KEY|PRIVATE_KEY|STORE_KEY|DATABASE_URL|DSN)[A-Z0-9_]*)(["'\s:=]+)([^"',\s}]+)`),
	regexp.MustCompile(`(?i)\b(authorization|api[_-]?key|access[_-]?token|refresh[_-]?token|auth[_-]?token|jwt[_-]?secret|password|secret)(["'\s:=]+)([^"',\s}]+)`),
}
var commandRawSecretPattern = regexp.MustCompile(`\b(sk-ant-[A-Za-z0-9_\-]{16,}|sk-[A-Za-z0-9_\-]{20,}|sk-or-[A-Za-z0-9_\-]{16,}|AIza[A-Za-z0-9_\-]{16,}|ghp_[A-Za-z0-9_]{20,})\b`)

func GetCommandDefinitions() []CommandDefinition {
	specs := commandSpecs()
	defs := make([]CommandDefinition, 0, len(specs))
	for _, spec := range specs {
		defs = append(defs, spec.CommandDefinition)
	}
	sort.Slice(defs, func(i, j int) bool {
		return strings.Join(defs[i].Path, " ") < strings.Join(defs[j].Path, " ")
	})
	return defs
}

func RunCommand(ctx context.Context, req CommandRunRequest) (CommandRunResult, error) {
	specs := commandSpecs()
	spec, ok := specs[req.ID]
	if !ok {
		return CommandRunResult{ID: req.ID, Error: "unknown_command"}, fmt.Errorf("command tidak dikenal: %s", req.ID)
	}
	result := CommandRunResult{ID: spec.ID}
	if !spec.Runnable || spec.Safety == CommandSafetyBlocked {
		result.Error = "blocked_by_policy"
		return result, nil
	}
	if spec.Safety == CommandSafetyMutating && !req.Confirm {
		result.ConfirmationRequired = true
		result.Error = "confirmation_required"
		return result, nil
	}
	if spec.build == nil {
		result.Error = "not_runnable"
		return result, nil
	}
	argv, err := spec.build(req.Args)
	if err != nil {
		result.Error = err.Error()
		return result, err
	}
	if len(argv) == 0 || argv[0] != "openclaw" {
		result.Error = "invalid_command_builder"
		return result, errors.New("invalid command builder")
	}
	result.Argv = argv

	runCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(runCtx, argv[0], argv[1:]...)
	cmd.Env = safeCommandEnv()
	out, err := cmd.CombinedOutput()
	if err != nil {
		if runCtx.Err() == context.DeadlineExceeded {
			result.Error = "timeout"
			result.ExitCode = 124
			result.Stderr = "command timed out after 15s"
			return result, nil
		}
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			result.ExitCode = exitErr.ExitCode()
		} else {
			result.ExitCode = 1
		}
		result.Stderr = RedactCommandOutput(strings.TrimSpace(string(out)))
		result.Error = err.Error()
		return result, nil
	}
	result.Stdout = RedactCommandOutput(strings.TrimSpace(string(out)))
	result.ExitCode = 0
	return result, nil
}

func RedactCommandOutput(text string) string {
	out := strings.ReplaceAll(text, "\r", " ")
	out = strings.ReplaceAll(out, "\x00", "")
	for _, re := range commandKeyValueSecretPatterns {
		out = re.ReplaceAllString(out, "$1$2[REDACTED]")
	}
	out = commandRawSecretPattern.ReplaceAllString(out, "[REDACTED]")
	if len(out) > 12000 {
		return out[:12000] + "...[truncated]"
	}
	return out
}

func safeCommandEnv() []string {
	allowed := []string{
		"PATH",
		"HOME",
		"USER",
		"LOGNAME",
		"SHELL",
		"TMPDIR",
		"TEMP",
		"TMP",
		"XDG_CACHE_HOME",
		"XDG_CONFIG_HOME",
		"XDG_DATA_HOME",
		"OPENCLAW_HOME",
		"OPENCLAW_GW_TOKEN",
		"OPENCLAW_GATEWAY_TOKEN",
	}
	env := make([]string, 0, len(allowed))
	for _, key := range allowed {
		if value, ok := os.LookupEnv(key); ok {
			env = append(env, key+"="+value)
		}
	}
	return env
}

func commandSpecs() map[string]commandSpec {
	specs := map[string]commandSpec{}
	add := func(id, category, safety, docs, description string, path []string, args []CommandArg, build func(map[string]string) ([]string, error)) {
		specs[id] = commandSpec{
			CommandDefinition: CommandDefinition{
				ID:          id,
				Path:        path,
				Category:    category,
				Safety:      safety,
				Runnable:    safety != CommandSafetyBlocked && build != nil,
				Description: description,
				DocsURL:     docs,
				Args:        args,
			},
			build: build,
		}
	}

	docs := "https://docs.openclaw.ai/cli"
	configDocs := "https://docs.openclaw.ai/cli/config"
	agentsDocs := "https://docs.openclaw.ai/cli/agents"
	modelsDocs := "https://docs.openclaw.ai/concepts/models"

	for _, item := range []struct {
		id, cat, desc string
		path          []string
	}{
		{"doctor", "diagnostics", "Run OpenClaw diagnostics.", []string{"doctor"}},
		{"version", "diagnostics", "Show OpenClaw version.", []string{"--version"}},
		{"help", "diagnostics", "Show OpenClaw help.", []string{"--help"}},
		{"config_validate", "config", "Validate OpenClaw config.", []string{"config", "validate"}},
		{"config_schema", "config", "Print config schema.", []string{"config", "schema"}},
		{"channels_list", "channels", "List configured channels.", []string{"channels", "list"}},
		{"agents_list", "agents", "List agents.", []string{"agents", "list"}},
		{"models_list", "models", "List available models.", []string{"models", "list"}},
		{"models_status", "models", "Show model/provider status.", []string{"models", "status"}},
		{"gateway_status", "gateway", "Show gateway status.", []string{"gateway", "status"}},
		{"daemon_status", "daemon", "Show daemon status.", []string{"daemon", "status"}},
		{"skills_list", "skills", "List installed skills.", []string{"skills", "list"}},
		{"plugins_list", "plugins", "List installed plugins.", []string{"plugins", "list"}},
		{"memory_status", "memory", "Show memory status.", []string{"memory", "status"}},
		{"sessions_list", "sessions", "List sessions.", []string{"sessions", "list"}},
		{"tasks_list", "tasks", "List tasks.", []string{"tasks", "list"}},
		{"sandbox_status", "sandbox", "Show sandbox status.", []string{"sandbox", "status"}},
		{"nodes_list", "nodes", "List nodes.", []string{"nodes", "list"}},
		{"secrets_list", "secrets", "List secret references.", []string{"secrets", "list"}},
	} {
		path := item.path
		add(item.id, item.cat, CommandSafetyReadOnly, docsFor(item.cat, docs, configDocs, agentsDocs, modelsDocs), item.desc, path, nil, func(map[string]string) ([]string, error) {
			return append([]string{"openclaw"}, path...), nil
		})
	}

	keyArg := []CommandArg{{Name: "key", Label: "Key", Required: true, Placeholder: "gateway.port"}}
	valueArg := []CommandArg{{Name: "key", Label: "Key", Required: true, Placeholder: "gateway.port"}, {Name: "value", Label: "Value", Required: true, Placeholder: "14000"}}
	modelArg := []CommandArg{{Name: "model", Label: "Model", Required: true, Placeholder: "ollama/qwen2.5:7b"}}
	agentArg := []CommandArg{{Name: "agent", Label: "Agent", Required: true, Placeholder: "main"}}
	providerArg := []CommandArg{{Name: "provider", Label: "Provider", Required: true, Placeholder: "openrouter"}}

	add("config_get", "config", CommandSafetyReadOnly, configDocs, "Get a config value.", []string{"config", "get"}, keyArg, func(args map[string]string) ([]string, error) {
		return argvWithSafeConfigKey([]string{"openclaw", "config", "get"}, args, "key")
	})
	add("models_info", "models", CommandSafetyReadOnly, modelsDocs, "Show model details.", []string{"models", "info"}, modelArg, func(args map[string]string) ([]string, error) {
		return argvWithRequired([]string{"openclaw", "models", "info"}, args, "model")
	})
	add("agents_info", "agents", CommandSafetyReadOnly, agentsDocs, "Show agent details.", []string{"agents", "info"}, agentArg, func(args map[string]string) ([]string, error) {
		return argvWithRequired([]string{"openclaw", "agents", "info"}, args, "agent")
	})
	add("config_show", "config", CommandSafetyBlocked, configDocs, "Resolved config output may include secrets; use config get for non-sensitive keys.", []string{"config", "show"}, nil, nil)
	add("secrets_get", "secrets", CommandSafetyBlocked, docs, "Secret value reads are blocked in dashboard runner.", []string{"secrets", "get"}, keyArg, nil)

	add("config_set", "config", CommandSafetyMutating, configDocs, "Set a config value.", []string{"config", "set"}, valueArg, func(args map[string]string) ([]string, error) {
		return argvWithRequired([]string{"openclaw", "config", "set"}, args, "key", "value")
	})
	add("config_unset", "config", CommandSafetyMutating, configDocs, "Unset a config value.", []string{"config", "unset"}, keyArg, func(args map[string]string) ([]string, error) {
		return argvWithRequired([]string{"openclaw", "config", "unset"}, args, "key")
	})
	add("models_set", "models", CommandSafetyMutating, modelsDocs, "Set the primary model.", []string{"models", "set"}, modelArg, func(args map[string]string) ([]string, error) {
		return argvWithRequired([]string{"openclaw", "models", "set"}, args, "model")
	})
	add("models_fallbacks", "models", CommandSafetyMutating, modelsDocs, "Set model fallbacks.", []string{"models", "fallbacks"}, []CommandArg{{Name: "models", Label: "Models", Required: true, Placeholder: "provider/model,provider/model2"}}, func(args map[string]string) ([]string, error) {
		return argvWithRequired([]string{"openclaw", "models", "fallbacks"}, args, "models")
	})
	add("agents_add", "agents", CommandSafetyMutating, agentsDocs, "Add an agent.", []string{"agents", "add"}, agentArg, func(args map[string]string) ([]string, error) {
		return argvWithRequired([]string{"openclaw", "agents", "add"}, args, "agent")
	})
	add("agents_delete", "agents", CommandSafetyMutating, agentsDocs, "Delete an agent.", []string{"agents", "delete"}, agentArg, func(args map[string]string) ([]string, error) {
		return argvWithRequired([]string{"openclaw", "agents", "delete"}, args, "agent")
	})
	add("agents_bind", "agents", CommandSafetyMutating, agentsDocs, "Bind an agent to a channel/account.", []string{"agents", "bind"}, append(agentArg, CommandArg{Name: "channel", Label: "Channel", Required: true, Placeholder: "telegram"}), func(args map[string]string) ([]string, error) {
		return argvWithRequired([]string{"openclaw", "agents", "bind"}, args, "agent", "channel")
	})
	add("agents_unbind", "agents", CommandSafetyMutating, agentsDocs, "Unbind an agent.", []string{"agents", "unbind"}, append(agentArg, CommandArg{Name: "channel", Label: "Channel", Required: true, Placeholder: "telegram"}), func(args map[string]string) ([]string, error) {
		return argvWithRequired([]string{"openclaw", "agents", "unbind"}, args, "agent", "channel")
	})
	add("gateway_start", "gateway", CommandSafetyMutating, docs, "Start gateway.", []string{"gateway", "start"}, nil, fixed("openclaw", "gateway", "start"))
	add("gateway_stop", "gateway", CommandSafetyMutating, docs, "Stop gateway.", []string{"gateway", "stop"}, nil, fixed("openclaw", "gateway", "stop"))
	add("gateway_restart", "gateway", CommandSafetyMutating, docs, "Restart gateway.", []string{"gateway", "restart"}, nil, fixed("openclaw", "gateway", "restart"))
	add("daemon_start", "daemon", CommandSafetyMutating, docs, "Start daemon.", []string{"daemon", "start"}, nil, fixed("openclaw", "daemon", "start"))
	add("daemon_stop", "daemon", CommandSafetyMutating, docs, "Stop daemon.", []string{"daemon", "stop"}, nil, fixed("openclaw", "daemon", "stop"))
	add("daemon_restart", "daemon", CommandSafetyMutating, docs, "Restart daemon.", []string{"daemon", "restart"}, nil, fixed("openclaw", "daemon", "restart"))
	add("skills_install", "skills", CommandSafetyMutating, docs, "Install a skill.", []string{"skills", "install"}, []CommandArg{{Name: "name", Label: "Name", Required: true, Placeholder: "skill-name"}}, func(args map[string]string) ([]string, error) {
		return argvWithRequired([]string{"openclaw", "skills", "install"}, args, "name")
	})
	add("skills_update", "skills", CommandSafetyMutating, docs, "Update a skill.", []string{"skills", "update"}, []CommandArg{{Name: "name", Label: "Name", Required: true, Placeholder: "skill-name"}}, func(args map[string]string) ([]string, error) {
		return argvWithRequired([]string{"openclaw", "skills", "update"}, args, "name")
	})
	add("plugins_install", "plugins", CommandSafetyMutating, docs, "Install a plugin.", []string{"plugins", "install"}, []CommandArg{{Name: "name", Label: "Name", Required: true, Placeholder: "plugin-name"}}, func(args map[string]string) ([]string, error) {
		return argvWithRequired([]string{"openclaw", "plugins", "install"}, args, "name")
	})
	add("plugins_enable", "plugins", CommandSafetyMutating, docs, "Enable a plugin.", []string{"plugins", "enable"}, []CommandArg{{Name: "name", Label: "Name", Required: true, Placeholder: "plugin-name"}}, func(args map[string]string) ([]string, error) {
		return argvWithRequired([]string{"openclaw", "plugins", "enable"}, args, "name")
	})
	add("plugins_disable", "plugins", CommandSafetyMutating, docs, "Disable a plugin.", []string{"plugins", "disable"}, []CommandArg{{Name: "name", Label: "Name", Required: true, Placeholder: "plugin-name"}}, func(args map[string]string) ([]string, error) {
		return argvWithRequired([]string{"openclaw", "plugins", "disable"}, args, "name")
	})
	add("memory_index", "memory", CommandSafetyMutating, docs, "Index memory.", []string{"memory", "index"}, nil, fixed("openclaw", "memory", "index"))
	add("sessions_cleanup", "sessions", CommandSafetyMutating, docs, "Cleanup sessions.", []string{"sessions", "cleanup"}, nil, fixed("openclaw", "sessions", "cleanup"))
	add("secrets_set", "secrets", CommandSafetyMutating, docs, "Set a secret reference.", []string{"secrets", "set"}, append(keyArg, CommandArg{Name: "value", Label: "Value", Required: true, Placeholder: "env:OPENAI_API_KEY"}), func(args map[string]string) ([]string, error) {
		return argvWithRequired([]string{"openclaw", "secrets", "set"}, args, "key", "value")
	})
	add("channels_enable", "channels", CommandSafetyMutating, docs, "Enable a channel provider.", []string{"channels", "enable"}, providerArg, func(args map[string]string) ([]string, error) {
		return argvWithRequired([]string{"openclaw", "channels", "enable"}, args, "provider")
	})
	add("channels_disable", "channels", CommandSafetyMutating, docs, "Disable a channel provider.", []string{"channels", "disable"}, providerArg, func(args map[string]string) ([]string, error) {
		return argvWithRequired([]string{"openclaw", "channels", "disable"}, args, "provider")
	})

	for _, blocked := range []struct {
		id, cat, desc string
		path          []string
	}{
		{"setup", "setup", "Interactive setup is blocked in dashboard runner.", []string{"setup"}},
		{"onboard", "setup", "Interactive onboarding is blocked in dashboard runner.", []string{"onboard"}},
		{"configure", "setup", "Interactive configure is blocked in dashboard runner.", []string{"configure"}},
		{"reset", "maintenance", "Reset is destructive and blocked.", []string{"reset"}},
		{"uninstall", "maintenance", "Uninstall is destructive and blocked.", []string{"uninstall"}},
		{"browser_open", "browser", "Browser automation is blocked.", []string{"browser", "open"}},
		{"browser_click", "browser", "Browser automation is blocked.", []string{"browser", "click"}},
		{"message_send", "message", "External message send is blocked.", []string{"message", "send"}},
		{"message_moderate", "message", "Moderation/destructive message action is blocked.", []string{"message", "moderate"}},
		{"security_apply", "security", "Security mutation is blocked here.", []string{"security", "apply"}},
		{"nodes_camera", "nodes", "Node camera access is blocked.", []string{"nodes", "camera"}},
		{"nodes_screen", "nodes", "Node screen access is blocked.", []string{"nodes", "screen"}},
		{"nodes_location", "nodes", "Node location access is blocked.", []string{"nodes", "location"}},
		{"tasks_run", "tasks", "Task execution may be long-running/interactive and is blocked.", []string{"tasks", "run"}},
		{"sandbox_exec", "sandbox", "Raw sandbox exec is blocked.", []string{"sandbox", "exec"}},
	} {
		add(blocked.id, blocked.cat, CommandSafetyBlocked, docs, blocked.desc, blocked.path, nil, nil)
	}

	return specs
}

func docsFor(category, docs, configDocs, agentsDocs, modelsDocs string) string {
	switch category {
	case "config":
		return configDocs
	case "agents":
		return agentsDocs
	case "models":
		return modelsDocs
	default:
		return docs
	}
}

func fixed(argv ...string) func(map[string]string) ([]string, error) {
	return func(map[string]string) ([]string, error) {
		return append([]string{}, argv...), nil
	}
}

func argvWithRequired(prefix []string, args map[string]string, required ...string) ([]string, error) {
	argv := append([]string{}, prefix...)
	for _, key := range required {
		value := strings.TrimSpace(args[key])
		if value == "" {
			return nil, fmt.Errorf("argumen wajib kosong: %s", key)
		}
		if !safeToken.MatchString(value) {
			return nil, fmt.Errorf("argumen %s mengandung karakter tidak aman", key)
		}
		argv = append(argv, value)
	}
	return argv, nil
}

func argvWithSafeConfigKey(prefix []string, args map[string]string, key string) ([]string, error) {
	value := strings.TrimSpace(args[key])
	if sensitiveConfigKey.MatchString(value) {
		return nil, fmt.Errorf("sensitive_config_key_blocked")
	}
	return argvWithRequired(prefix, args, key)
}
