package config

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"
	rungrad "github.com/vincentsch/rungrad"
)

// ProjectConfigPath returns the path from --config, or ./llmention.yaml when
// the flag is not set.
func ProjectConfigPath(cmd *cobra.Command, _ *rungrad.Factory) (string, error) {
	if cmd != nil {
		root := cmd.Root()
		if root != nil {
			flag := root.PersistentFlags().Lookup("config")
			if flag != nil && flag.Changed {
				value := strings.TrimSpace(flag.Value.String())
				if value == "" {
					return "", fmt.Errorf("--config must not be empty")
				}
				return value, nil
			}
		}
	}
	return DefaultProjectConfigPath, nil
}

// LoadProject loads the project file the command points at.
func LoadProject(cmd *cobra.Command, f *rungrad.Factory) (*Project, error) {
	path, err := ProjectConfigPath(cmd, f)
	if err != nil {
		return nil, err
	}
	return Load(path)
}
