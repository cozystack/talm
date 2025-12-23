package generator

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/cozystack/talm/pkg/generated"
	"gopkg.in/yaml.v3"

	"github.com/siderolabs/talos/pkg/machinery/config/generate/secrets"
)

func writeSecretsBundle(opts Options, bundle *secrets.Bundle) error {
	bytes, err := yaml.Marshal(bundle)
	if err != nil {
		return err
	}

	dest := filepath.Join(opts.RootDir, "secrets.yaml")
	return writeFile(opts, dest, bytes)
}

func writeFile(opts Options, dest string, content []byte) error {
	if !opts.Force {
		if _, err := os.Stat(dest); err == nil {
			return fmt.Errorf("%s already exists (use Force=true)", dest)
		}
	}

	if err := os.MkdirAll(filepath.Dir(dest), os.ModePerm); err != nil {
		return fmt.Errorf("failed to create dir: %w", err)
	}

	if err := os.WriteFile(dest, content, 0o644); err != nil {
		return fmt.Errorf("failed to write file: %w", err)
	}

	fmt.Fprintf(os.Stderr, "Created %s\n", dest)
	return nil
}

func writePresets(opts Options, clusterName string) error {
	for path, content := range generated.PresetFiles {

		parts := strings.SplitN(path, "/", 2)
		chartName := parts[0]

		if chartName != opts.Preset && chartName != "talm" {
			continue
		}

		out := filepath.Join(opts.RootDir, parts[1])

		// Template Chart.yaml
		if strings.HasSuffix(path, "Chart.yaml") {
			content = fmt.Sprintf(content, clusterName, "0.1.0")
		}

		if err := writeFile(opts, out, []byte(content)); err != nil {
			return err
		}
	}
	return nil
}
