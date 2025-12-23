package generator

import (
	"fmt"
	"path/filepath"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/siderolabs/talos/cmd/talosctl/cmd/mgmt/gen"
	"github.com/siderolabs/talos/pkg/machinery/config"
	"github.com/siderolabs/talos/pkg/machinery/config/generate"
	"github.com/siderolabs/talos/pkg/machinery/config/generate/secrets"
)

type Options struct {
	RootDir      string
	TalosVersion string
	Preset       string
	Force        bool

	APIServerURL string
}

// Run performs full generation exactly like "talm init"
func Run(opts Options) error {
	var (
		contract      *config.VersionContract
		secretsBundle *secrets.Bundle
		err           error
	)

	// Version contract
	if opts.TalosVersion != "" {
		contract, err = config.ParseContractFromVersion(opts.TalosVersion)
		if err != nil {
			return fmt.Errorf("invalid talos-version: %w", err)
		}
	}

	// Secrets bundle
	secretsBundle, err = secrets.NewBundle(secrets.NewFixedClock(time.Now()), contract)
	if err != nil {
		return fmt.Errorf("failed to create secrets bundle: %w", err)
	}

	// Write secrets.yaml
	if err := writeSecretsBundle(opts, secretsBundle); err != nil {
		return err
	}

	// Cluster name = name of directory
	absolutePath, err := filepath.Abs(opts.RootDir)
	if err != nil {
		return err
	}
	clusterName := filepath.Base(absolutePath)

	// Config generation
	var genOptions []generate.Option
	genOptions = append(genOptions, generate.WithSecretsBundle(secretsBundle))

	if contract != nil {
		genOptions = append(genOptions, generate.WithVersionContract(contract))
	}

	if opts.APIServerURL == "" {
		opts.APIServerURL = "https://192.168.0.1:6443"
	}

	configBundle, err := gen.GenerateConfigBundle(
		genOptions,
		clusterName,
		opts.APIServerURL,
		"",
		[]string{},
		[]string{},
		[]string{},
	)
	if err != nil {
		return err
	}

	configBundle.TalosConfig().Contexts[clusterName].Endpoints = []string{"127.0.0.1"}

	// Write talosconfig
	content, err := yaml.Marshal(configBundle.TalosConfig())
	if err != nil {
		return err
	}

	if err := writeFile(opts, filepath.Join(opts.RootDir, "talosconfig"), content); err != nil {
		return err
	}

	// Write preset files
	if err := writePresets(opts, clusterName); err != nil {
		return err
	}

	return nil
}
