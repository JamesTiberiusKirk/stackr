package stackcmd

import (
	"errors"
	"fmt"
	"io/fs"
	"log"
	"os"
	"regexp"
	"strconv"
	"strings"

	"github.com/joho/godotenv"
	"gopkg.in/yaml.v3"

	"github.com/jamestiberiuskirk/stackr/internal/compose"
	"github.com/jamestiberiuskirk/stackr/internal/config"
)

// AutoDeployLabel is the docker-compose service label that opts a service in
// or out of automated deployments. Missing label defaults to enabled.
const AutoDeployLabel = "stackr.deploy.auto"

type autoDeployComposeFile struct {
	Services map[string]autoDeployComposeService `yaml:"services"`
}

type autoDeployComposeService struct {
	Labels compose.LabelMap `yaml:"labels"`
}

// IsAutoDeployEnabled reports whether auto-deployment is enabled for the named
// stack. A stack is considered disabled if ANY service in its primary compose
// file has stackr.deploy.auto=false (or an env-var that resolves to false).
// Stacks with no auto-deploy label default to enabled.
func IsAutoDeployEnabled(cfg config.Config, stackName string) (bool, error) {
	info, err := ResolveStackPath(cfg, stackName)
	if err != nil {
		return false, err
	}
	if len(info.ComposePaths) == 0 {
		return false, fmt.Errorf("no compose files configured for stack %q", stackName)
	}

	content, err := os.ReadFile(info.ComposePaths[0])
	if err != nil {
		return false, fmt.Errorf("read compose file: %w", err)
	}

	var parsed autoDeployComposeFile
	if err := yaml.Unmarshal(content, &parsed); err != nil {
		return false, fmt.Errorf("parse compose file: %w", err)
	}

	envVars, err := godotenv.Read(cfg.EnvFile)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		log.Printf("warning: read env file %s: %v", cfg.EnvFile, err)
	}
	if envVars == nil {
		envVars = map[string]string{}
	}

	for serviceName, service := range parsed.Services {
		labelValue, hasLabel := service.Labels[AutoDeployLabel]
		if !hasLabel {
			continue
		}
		resolved := strings.TrimSpace(resolveEnvRefs(labelValue, envVars))
		enabled, err := strconv.ParseBool(resolved)
		if err != nil {
			log.Printf("warning: invalid %s value for stack=%s service=%s: %q, treating as disabled",
				AutoDeployLabel, stackName, serviceName, resolved)
			return false, nil
		}
		if !enabled {
			return false, nil
		}
	}

	return true, nil
}

var envRefPattern = regexp.MustCompile(`\$\{([^}]+)\}`)

// resolveEnvRefs expands ${VAR} references using envVars; unresolved refs are
// left intact so the caller can still parse them or surface them in an error.
func resolveEnvRefs(value string, envVars map[string]string) string {
	return envRefPattern.ReplaceAllStringFunc(value, func(match string) string {
		name := match[2 : len(match)-1]
		if v, ok := envVars[name]; ok {
			return v
		}
		return match
	})
}
