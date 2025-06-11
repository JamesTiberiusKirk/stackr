package composeconvert

import (
	"context"
	"fmt"
	"maps"
	"os"
	"sort"
	"strconv"
	"strings"

	"github.com/compose-spec/compose-go/loader"
	"github.com/compose-spec/compose-go/types"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/api/types/strslice"
	"github.com/docker/go-connections/nat"
	"gopkg.in/yaml.v3"
)

type LoadComposeProjectOptions struct {
	DockerFilePath string
	NamePrefix     string
	NameSuffix     string
	// This will overwrite any existing env pulled from system (if its enabled)
	Env               map[string]string
	PullEnvFromSystem bool
	WorkingDir        string
}

func LoadComposeStack(ctx context.Context, ops LoadComposeProjectOptions) (*types.Project, error) {
	file, err := os.Open(ops.DockerFilePath)
	if err != nil {
		return nil, fmt.Errorf("failed to open compose file: %w", err)
	}
	defer file.Close()

	var raw map[string]any
	decoder := yaml.NewDecoder(file)
	if err := decoder.Decode(&raw); err != nil {
		return nil, fmt.Errorf("failed to decode YAML: %w", err)
	}

	env := map[string]string{}
	if ops.PullEnvFromSystem {
		for _, e := range os.Environ() {
			parts := strings.SplitN(e, "=", 2)
			if len(parts) == 2 {
				env[parts[0]] = parts[1]
			}
		}
	}

	// copy all the k/v pairs from ops to env map
	maps.Copy(env, ops.Env)

	workingDir := ops.WorkingDir
	if workingDir != "" {
		pwd, err := os.Getwd()
		if err != nil {
			return nil, fmt.Errorf("failed to get pwd: %w", err)
		}

		workingDir = pwd
	}

	project, err := loader.Load(types.ConfigDetails{
		WorkingDir: workingDir,
		ConfigFiles: []types.ConfigFile{
			{Filename: ops.DockerFilePath, Config: raw},
		},
		Environment: env,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to load compose project: %w", err)
	}

	if ops.NamePrefix != "" || ops.NameSuffix != "" {
		for i, _ := range project.Services {
			if ops.NamePrefix != "" {
				project.Services[i].Name = ops.NamePrefix + project.Services[i].Name
			}

			if ops.NameSuffix != "" {
				project.Services[i].Name = project.Services[i].Name + ops.NameSuffix
			}
		}
	}

	orderedServices, err := topoSortServices(project.Services)
	if err != nil {
		return nil, fmt.Errorf("failed to order services: %w", err)
	}
	project.Services = orderedServices

	return project, nil
}

func TranslateServiceConfigToContainerConfig(service types.ServiceConfig) (*container.Config, *container.HostConfig, *network.NetworkingConfig, error) {
	envVars := []string{}
	for key, val := range service.Environment {
		envVars = append(envVars, fmt.Sprintf("%s=%s", key, *val))
	}

	config := &container.Config{
		Image:    service.Image,
		Env:      envVars,
		Cmd:      strslice.StrSlice(service.Command),
		Labels:   service.Labels,
		Hostname: service.Name,
	}

	hostConfig := &container.HostConfig{
		RestartPolicy: container.RestartPolicy{
			Name: container.RestartPolicyMode(service.Restart),
		},
		Binds: []string{},
	}

	for _, vol := range service.Volumes {
		if vol.Source != "" && vol.Target != "" {
			hostConfig.Binds = append(hostConfig.Binds, fmt.Sprintf("%s:%s", vol.Source, vol.Target))
		}
	}

	portBindings := nat.PortMap{}
	exposedPorts := nat.PortSet{}

	for _, port := range service.Ports {
		protocol := string(port.Protocol)
		if protocol == "" {
			protocol = "tcp"
		}

		portKey := nat.Port(fmt.Sprintf("%d/%s", port.Target, protocol))
		exposedPorts[portKey] = struct{}{}

		if port.Published != "" {
			publishedPort, err := strconv.Atoi(port.Published)
			if err != nil {
				return nil, nil, nil, fmt.Errorf("invalid published port %q: %w", port.Published, err)
			}

			binding := nat.PortBinding{
				HostPort: fmt.Sprintf("%d", publishedPort),
				HostIP:   port.HostIP,
			}
			portBindings[portKey] = append(portBindings[portKey], binding)
		}
	}

	config.ExposedPorts = exposedPorts
	hostConfig.PortBindings = portBindings
	networkConfig := &network.NetworkingConfig{}

	return config, hostConfig, networkConfig, nil
}

func topoSortServices(services []types.ServiceConfig) ([]types.ServiceConfig, error) {
	graph := map[string][]string{}
	inDegree := map[string]int{}
	serviceMap := map[string]types.ServiceConfig{}

	for _, svc := range services {
		serviceMap[svc.Name] = svc
		inDegree[svc.Name] = 0
	}

	for _, svc := range services {
		for dep := range svc.DependsOn {
			graph[dep] = append(graph[dep], svc.Name)
			inDegree[svc.Name]++
		}
	}

	var queue []string
	for name, degree := range inDegree {
		if degree == 0 {
			queue = append(queue, name)
		}
	}
	sort.Strings(queue)

	var result []types.ServiceConfig
	for len(queue) > 0 {
		curr := queue[0]
		queue = queue[1:]

		result = append(result, serviceMap[curr])

		for _, neighbor := range graph[curr] {
			inDegree[neighbor]--
			if inDegree[neighbor] == 0 {
				queue = append(queue, neighbor)
				sort.Strings(queue)
			}
		}
	}

	if len(result) != len(services) {
		return nil, fmt.Errorf("circular or unresolved dependencies detected")
	}

	return result, nil
}
