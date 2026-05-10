package modeline

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/cockroachdb/errors"
)

// Config structure for storing settings from modeline.
type Config struct {
	Nodes     []string
	Endpoints []string
	Templates []string
}

// ParseModeline parses a modeline string and populates the Config structure.
func ParseModeline(line string) (*Config, error) {
	config := &Config{}
	trimLine := strings.TrimSpace(line)

	prefix := "# talm: "
	if after, ok := strings.CutPrefix(trimLine, prefix); ok {
		content := after

		parts := strings.SplitSeq(content, ", ")
		for part := range parts {
			keyVal := strings.SplitN(strings.TrimSpace(part), "=", 2)
			if len(keyVal) != 2 {
				//nolint:wrapcheck // cockroachdb/errors.WithHintf is the project's wrapping/hinting idiom
				return nil, errors.WithHintf(
					errors.Newf("invalid format of modeline part: %s", part),
					"expected key=value form (value is a JSON array); see modeline contract",
				)
			}

			key := keyVal[0]
			val := keyVal[1]

			var arr []string

			err := json.Unmarshal([]byte(val), &arr)
			if err != nil {
				//nolint:wrapcheck // cockroachdb/errors.WithHintf is the project's wrapping/hinting idiom
				return nil, errors.WithHintf(
					errors.Wrapf(err, "error parsing JSON array for key %s, value %s", key, val),
					"value must be a JSON array, e.g. nodes=[\"1.2.3.4\"]",
				)
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

	//nolint:wrapcheck // cockroachdb/errors.WithHint is the project's wrapping/hinting idiom
	return nil, errors.WithHint(
		errors.New("modeline prefix not found"),
		"first line must begin with '# talm: '",
	)
}

// ReadAndParseModeline reads the first line from a file and parses the modeline.
func ReadAndParseModeline(filePath string) (*Config, error) {
	file, err := os.Open(filePath)
	if err != nil {
		//nolint:wrapcheck // cockroachdb/errors.WithHintf is the project's wrapping/hinting idiom
		return nil, errors.WithHintf(
			errors.Wrap(err, "error opening config file"),
			"check that %s exists and is readable", filePath,
		)
	}
	defer func() { _ = file.Close() }()

	scanner := bufio.NewScanner(file)
	if scanner.Scan() {
		firstLine := scanner.Text()

		return ParseModeline(firstLine)
	}

	err = scanner.Err()
	if err != nil {
		//nolint:wrapcheck // cockroachdb/errors.WithHint is the project's wrapping/hinting idiom
		return nil, errors.WithHint(
			errors.Wrap(err, "error reading first line of config file"),
			"file may be truncated or unreadable",
		)
	}

	//nolint:wrapcheck // cockroachdb/errors.WithHint is the project's wrapping/hinting idiom
	return nil, errors.WithHint(
		errors.New("config file is empty"),
		"per-node values file must start with a modeline like '# talm: nodes=[...]'",
	)
}

// GenerateModeline creates a modeline string using JSON formatting for values.
func GenerateModeline(nodes, endpoints, templates []string) (string, error) {
	// Convert Nodes to JSON
	nodesJSON, err := json.Marshal(nodes)
	if err != nil {
		return "", errors.Wrap(err, "failed to marshal nodes")
	}

	// Convert Endpoints to JSON
	endpointsJSON, err := json.Marshal(endpoints)
	if err != nil {
		return "", errors.Wrap(err, "failed to marshal endpoints")
	}

	// Convert Templates to JSON
	templatesJSON, err := json.Marshal(templates)
	if err != nil {
		return "", errors.Wrap(err, "failed to marshal templates")
	}

	// Form the final modeline string
	modeline := fmt.Sprintf(`# talm: nodes=%s, endpoints=%s, templates=%s`, string(nodesJSON), string(endpointsJSON), string(templatesJSON))

	return modeline, nil
}
