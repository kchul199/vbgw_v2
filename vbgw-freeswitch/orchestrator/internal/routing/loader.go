package routing

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// ValidateOptions define what identifiers and ingress stages are valid in this deployment.
type ValidateOptions struct {
	AllowedGatewayIDs               []string
	AllowedIngressStages            []string
	RequireSourceGatewaysForEntries []string
}

// LoadFromPath reads, validates, and normalizes a routing config file.
func LoadFromPath(path string, opts ValidateOptions) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read routing config: %w", err)
	}

	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse routing config: %w", err)
	}

	if err := Normalize(&cfg); err != nil {
		return nil, err
	}
	if err := Validate(&cfg, opts); err != nil {
		return nil, err
	}

	return &cfg, nil
}
