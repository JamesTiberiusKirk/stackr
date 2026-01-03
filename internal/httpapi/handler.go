package httpapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/jamestiberiuskirk/stackr/internal/config"
	"github.com/jamestiberiuskirk/stackr/internal/runner"
	"gopkg.in/yaml.v3"
)

var semverPattern = regexp.MustCompile(`^v\d+\.\d+\.\d+(-[a-zA-Z0-9._-]+)?$`)

const autoDeployLabel = "stackr.deploy.auto"

type Handler struct {
	cfg    config.Config
	runner *runner.Runner
	mux    *http.ServeMux
}

type composeFile struct {
	Services map[string]composeService `yaml:"services"`
}

type composeService struct {
	Labels labelMap `yaml:"labels"`
}

type labelMap map[string]string

func (l *labelMap) UnmarshalYAML(value *yaml.Node) error {
	result := make(map[string]string)
	if value == nil || value.Kind == 0 {
		*l = result
		return nil
	}

	switch value.Kind {
	case yaml.SequenceNode:
		for _, item := range value.Content {
			parts := strings.SplitN(item.Value, "=", 2)
			if len(parts) != 2 {
				continue
			}
			result[strings.TrimSpace(parts[0])] = strings.TrimSpace(parts[1])
		}
	case yaml.MappingNode:
		for i := 0; i < len(value.Content); i += 2 {
			key := strings.TrimSpace(value.Content[i].Value)
			if i+1 >= len(value.Content) {
				continue
			}
			result[key] = strings.TrimSpace(value.Content[i+1].Value)
		}
	default:
		return fmt.Errorf("unsupported labels format: %s", value.ShortTag())
	}

	*l = result
	return nil
}

type deployRequest struct {
	Stack    string `json:"stack"`
	Tag      string `json:"tag"`
	ImageTag string `json:"image_tag"`
}

func New(cfg config.Config, runner *runner.Runner) http.Handler {
	h := &Handler{cfg: cfg, runner: runner}
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", h.handleHealth)
	mux.HandleFunc("/deploy", h.handleDeploy)
	h.mux = mux
	return h
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.mux.ServeHTTP(w, r)
}

func (h *Handler) handleHealth(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (h *Handler) handleDeploy(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}

	if !h.authorize(r.Header.Get("Authorization")) {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "missing or invalid token"})
		return
	}

	payload, err := decodeDeployRequest(r.Body)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	stackName := payload.Stack
	if stackName == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "stack is required"})
		return
	}

	if err := h.ensureStackExists(stackName); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	// Check if auto-deployment is enabled for this stack
	enabled, err := h.isAutoDeployEnabled(stackName)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": fmt.Sprintf("failed to check auto-deploy status: %v", err)})
		return
	}
	if !enabled {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "auto-deployment is disabled for this stack"})
		return
	}

	// Create default stack config for deployment
	stackCfg := config.StackConfig{
		TagEnv: strings.ToUpper(stackName) + "_IMAGE_TAG",
		Args:   []string{stackName, "update"},
	}

	tag := payload.Tag
	if tag == "" {
		tag = payload.ImageTag
	}
	tag = strings.TrimSpace(tag)
	if tag == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "tag is required"})
		return
	}

	// Validate tag: must be "latest" or semver format (v1.2.3 or v1.2.3-prerelease)
	if tag != "latest" && !semverPattern.MatchString(tag) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "tag must be 'latest' or semver format (v1.2.3 or v1.2.3-prerelease)"})
		return
	}

	result, err := h.runner.Deploy(r.Context(), stackName, stackCfg, tag)
	if err != nil {
		var cmdErr *runner.CommandError
		if errors.As(err, &cmdErr) {
			writeJSON(w, http.StatusInternalServerError, map[string]string{
				"error":     cmdErr.Msg,
				"exit_code": fmt.Sprintf("%d", cmdErr.Code),
				"stdout":    strings.TrimSpace(cmdErr.Stdout),
				"stderr":    strings.TrimSpace(cmdErr.Stderr),
			})
			return
		}

		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	writeJSON(w, http.StatusOK, result)
}

func decodeDeployRequest(body io.Reader) (deployRequest, error) {
	payload := deployRequest{}
	data, err := io.ReadAll(body)
	if err != nil {
		return payload, fmt.Errorf("failed to read request body")
	}

	if err := json.Unmarshal(data, &payload); err != nil {
		return payload, fmt.Errorf("invalid JSON body")
	}

	payload.Stack = strings.TrimSpace(payload.Stack)
	payload.Tag = strings.TrimSpace(payload.Tag)
	payload.ImageTag = strings.TrimSpace(payload.ImageTag)
	return payload, nil
}

func (h *Handler) authorize(header string) bool {
	if !strings.HasPrefix(header, "Bearer ") {
		return false
	}

	token := strings.TrimSpace(strings.TrimPrefix(header, "Bearer "))
	return token == h.cfg.Token
}

func writeJSON(w http.ResponseWriter, status int, payload interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)

	if payload == nil {
		return
	}

	if err := json.NewEncoder(w).Encode(payload); err != nil {
		log.Printf("failed to write JSON response: %v", err)
	}
}

func (h *Handler) ensureStackExists(name string) error {
	stackDir := filepath.Join(h.cfg.StacksDir, name)
	info, err := os.Stat(stackDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("stack %q does not exist", name)
		}
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("%s is not a stack directory", stackDir)
	}

	composePath := filepath.Join(stackDir, "docker-compose.yml")
	if _, err := os.Stat(composePath); err != nil {
		return fmt.Errorf("stack %q is missing docker-compose.yml", name)
	}
	return nil
}

// isAutoDeployEnabled checks if auto-deployment is enabled for a stack
// Returns false if ANY service has stackr.deploy.auto=false (or env var resolving to false)
// Defaults to true if label is not present
func (h *Handler) isAutoDeployEnabled(stackName string) (bool, error) {
	composePath := filepath.Join(h.cfg.StacksDir, stackName, "docker-compose.yml")
	content, err := os.ReadFile(composePath)
	if err != nil {
		return false, fmt.Errorf("failed to read compose file: %w", err)
	}

	var parsed composeFile
	if err := yaml.Unmarshal(content, &parsed); err != nil {
		return false, fmt.Errorf("failed to parse compose file: %w", err)
	}

	// Load .env file for variable resolution
	envVars, err := h.loadEnvFile()
	if err != nil {
		log.Printf("warning: failed to load .env file for auto-deploy check: %v", err)
		envVars = make(map[string]string)
	}

	// Check all services for stackr.deploy.auto label
	for serviceName, service := range parsed.Services {
		labelValue, hasLabel := service.Labels[autoDeployLabel]
		if !hasLabel {
			continue
		}

		// Resolve environment variable references like ${MYAPP_AUTODEPLOY}
		resolvedValue := h.resolveEnvVars(labelValue, envVars)
		resolvedValue = strings.TrimSpace(resolvedValue)

		// Parse as boolean
		enabled, err := strconv.ParseBool(resolvedValue)
		if err != nil {
			log.Printf("warning: invalid %s value for stack=%s service=%s: %q, treating as disabled",
				autoDeployLabel, stackName, serviceName, resolvedValue)
			return false, nil
		}

		if !enabled {
			log.Printf("auto-deployment disabled for stack=%s service=%s", stackName, serviceName)
			return false, nil
		}
	}

	return true, nil
}

// loadEnvFile reads the .env file and returns a map of environment variables
func (h *Handler) loadEnvFile() (map[string]string, error) {
	envPath := h.cfg.EnvFile
	if envPath == "" {
		envPath = filepath.Join(h.cfg.RepoRoot, ".env")
	}

	content, err := os.ReadFile(envPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return make(map[string]string), nil
		}
		return nil, err
	}

	envVars := make(map[string]string)
	lines := strings.Split(string(content), "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		parts := strings.SplitN(line, "=", 2)
		if len(parts) == 2 {
			key := strings.TrimSpace(parts[0])
			value := strings.TrimSpace(parts[1])
			// Remove surrounding quotes if present
			if len(value) >= 2 && ((value[0] == '"' && value[len(value)-1] == '"') ||
				(value[0] == '\'' && value[len(value)-1] == '\'')) {
				value = value[1 : len(value)-1]
			}
			envVars[key] = value
		}
	}

	return envVars, nil
}

// resolveEnvVars resolves ${VAR} references in a string using the provided env map
func (h *Handler) resolveEnvVars(value string, envVars map[string]string) string {
	// Simple regex-based replacement for ${VAR} patterns
	pattern := regexp.MustCompile(`\$\{([^}]+)\}`)
	return pattern.ReplaceAllStringFunc(value, func(match string) string {
		// Extract variable name from ${VAR}
		varName := match[2 : len(match)-1]
		if envValue, ok := envVars[varName]; ok {
			return envValue
		}
		// Return original if not found
		return match
	})
}
