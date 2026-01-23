package modeline

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

// Config structure for storing settings from modeline
type Config struct {
	Nodes     []string
	Endpoints []string
	Templates []string
}

// ParseModeline parses a modeline string and populates the Config structure
func ParseModeline(line string) (*Config, error) {
	config := &Config{}
	trimLine := strings.TrimSpace(line)
	prefix := "# talm: "
	if strings.HasPrefix(trimLine, prefix) {
		content := strings.TrimPrefix(trimLine, prefix)
		parts := strings.Split(content, ", ")
		for _, part := range parts {
			keyVal := strings.SplitN(strings.TrimSpace(part), "=", 2)
			if len(keyVal) != 2 {
				return nil, fmt.Errorf("invalid format of modeline part: %s", part)
			}
			key := keyVal[0]
			val := keyVal[1]
			var arr []string
			if err := json.Unmarshal([]byte(val), &arr); err != nil {
				return nil, fmt.Errorf("error parsing JSON array for key %s, value %s, error: %v", key, val, err)
			}
			// Assign values to Config fields based on known keys
			switch key {
			case "nodes":
				config.Nodes = arr
			case "endpoints":
				config.Endpoints = arr
			case "templates":
				config.Templates = arr
				// Ignore unknown keys
			}
		}
		return config, nil
	}
	return nil, fmt.Errorf("modeline prefix not found")
}

// ReadAndParseModeline reads the first line from a file and parses the modeline.
func ReadAndParseModeline(filePath string) (*Config, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return nil, fmt.Errorf("error opening config file: %v", err)
	}
	defer func() { _ = file.Close() }()

	scanner := bufio.NewScanner(file)
	if scanner.Scan() {
		firstLine := scanner.Text()
		return ParseModeline(firstLine)
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("error reading first line of config file: %v", err)
	}

	return nil, fmt.Errorf("config file is empty")
}

// GenerateModeline creates a modeline string using JSON formatting for values
func GenerateModeline(nodes []string, endpoints []string, templates []string) (string, error) {
	// Convert Nodes to JSON
	nodesJSON, err := json.Marshal(nodes)
	if err != nil {
		return "", fmt.Errorf("failed to marshal nodes: %v", err)
	}

	// Convert Endpoints to JSON
	endpointsJSON, err := json.Marshal(endpoints)
	if err != nil {
		return "", fmt.Errorf("failed to marshal endpoints: %v", err)
	}

	// Convert Templates to JSON
	templatesJSON, err := json.Marshal(templates)
	if err != nil {
		return "", fmt.Errorf("failed to marshal templates: %v", err)
	}

	// Form the final modeline string
	modeline := fmt.Sprintf(`# talm: nodes=%s, endpoints=%s, templates=%s`, string(nodesJSON), string(endpointsJSON), string(templatesJSON))
	return modeline, nil
}
