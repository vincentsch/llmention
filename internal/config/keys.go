package config

import (
	"fmt"
	"os"
	"strings"
)

// KeySelection says whose keys a command needs: the keys of some engines,
// and the AI grader's key when IncludeJudge is set. No engine ids means all
// engines.
type KeySelection struct {
	EngineIDs    []string
	IncludeJudge bool
}

// MissingKeyError names the key variables that are not set.
type MissingKeyError struct {
	EnvVars []string
}

func (e MissingKeyError) Error() string {
	if len(e.EnvVars) == 1 {
		return fmt.Sprintf("the API key %s is not set", e.EnvVars[0])
	}
	n := len(e.EnvVars)
	return fmt.Sprintf("the API keys %s and %s are not set", strings.Join(e.EnvVars[:n-1], ", "), e.EnvVars[n-1])
}

// RequiredKeyEnvVars lists the key variables a selection needs, without
// reading them.
func (cfg *Project) RequiredKeyEnvVars(selection KeySelection) ([]string, error) {
	return cfg.selectedKeyEnvVars(selection)
}

// ResolveKeys reads the selected keys from the environment. Each key goes to
// registerSecret, so it can be removed from everything llmention prints.
func (cfg *Project) ResolveKeys(selection KeySelection, lookupEnv func(string) (string, bool), registerSecret func(string)) (map[string]string, error) {
	envs, err := cfg.selectedKeyEnvVars(selection)
	if err != nil {
		return nil, err
	}
	if lookupEnv == nil {
		lookupEnv = os.LookupEnv
	}
	keys := make(map[string]string, len(envs))
	var missing []string
	for _, env := range envs {
		value, ok := lookupEnv(env)
		if !ok || value == "" {
			missing = append(missing, env)
			continue
		}
		if registerSecret != nil {
			registerSecret(value)
		}
		keys[env] = value
	}
	if len(missing) > 0 {
		return nil, MissingKeyError{EnvVars: missing}
	}
	return keys, nil
}

func (cfg *Project) selectedKeyEnvVars(selection KeySelection) ([]string, error) {
	if cfg == nil {
		return nil, fmt.Errorf("project config is nil")
	}
	engineByID := make(map[string]EngineConfig, len(cfg.Engines))
	for _, engine := range cfg.Engines {
		engineByID[engine.ID] = engine
	}
	var envs []string
	if len(selection.EngineIDs) == 0 && !selection.IncludeJudge {
		for _, engine := range cfg.Engines {
			envs = appendEnv(envs, engine.KeyEnv)
		}
	} else {
		for _, id := range selection.EngineIDs {
			engine, ok := engineByID[id]
			if !ok {
				return nil, fmt.Errorf("selected engine %q is not configured", id)
			}
			envs = appendEnv(envs, engine.KeyEnv)
		}
	}
	if selection.IncludeJudge && cfg.Analysis.Judge != nil {
		envs = appendEnv(envs, cfg.Analysis.Judge.KeyEnv)
	}
	return envs, nil
}

func appendEnv(envs []string, env string) []string {
	if env == "" {
		return envs
	}
	for _, existing := range envs {
		if existing == env {
			return envs
		}
	}
	return append(envs, env)
}
