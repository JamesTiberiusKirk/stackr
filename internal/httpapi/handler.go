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
	"strings"

	"github.com/jamestiberiuskirk/stackr/internal/config"
	"github.com/jamestiberiuskirk/stackr/internal/runner"
)

var semverPattern = regexp.MustCompile(`^v\d+\.\d+\.\d+(-[a-zA-Z0-9._-]+)?$`)

type Handler struct {
	cfg    config.Config
	runner *runner.Runner
	mux    *http.ServeMux
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
