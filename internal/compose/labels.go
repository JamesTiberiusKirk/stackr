package compose

import (
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

// LabelMap is a map of string key-value pairs that supports both YAML sequence
// (["key=value"]) and mapping ({key: value}) formats used by Docker Compose labels.
type LabelMap map[string]string

// UnmarshalYAML implements custom unmarshalling for Docker Compose label formats.
func (l *LabelMap) UnmarshalYAML(value *yaml.Node) error {
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
