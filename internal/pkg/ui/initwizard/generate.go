package initwizard

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/cozystack/talm/pkg/generated"
	"github.com/siderolabs/talos/cmd/talosctl/cmd/mgmt/gen"
	"github.com/siderolabs/talos/pkg/machinery/config"
	"github.com/siderolabs/talos/pkg/machinery/config/generate"
	"github.com/siderolabs/talos/pkg/machinery/config/generate/secrets"
	"gopkg.in/yaml.v3"
)

// GenerateFromTUI генерирует конфигурации из TUI
func GenerateFromTUI(data *InitData) error {
	log.Printf("DEBUG GenerateFromTUI: Starting with preset=%s, clusterName=%s", data.Preset, data.ClusterName)
	var (
		secretsBundle   *secrets.Bundle
		versionContract *config.VersionContract
		err             error
	)

	// 1. Validate preset
	log.Printf("DEBUG GenerateFromTUI: Validating preset: %s", data.Preset)
	if !isValidPreset(data.Preset) {
		return fmt.Errorf("invalid preset: %s. Valid presets: %s", data.Preset, generated.AvailablePresets)
	}
	log.Printf("DEBUG GenerateFromTUI: Preset valid")

	// 2. Parse Talos version
	if data.TalosVersion != "" {
		versionContract, err = config.ParseContractFromVersion(data.TalosVersion)
		if err != nil {
			return fmt.Errorf("invalid talos-version: %w", err)
		}
	}

	// 3. Create secrets bundle
	secretsBundle, err = secrets.NewBundle(secrets.NewFixedClock(time.Now()), versionContract)
	if err != nil {
		return fmt.Errorf("failed to create secrets bundle: %w", err)
	}

	// 4. Setup generation options
	genOptions := []generate.Option{generate.WithSecretsBundle(secretsBundle)}
	if versionContract != nil {
		genOptions = append(genOptions, generate.WithVersionContract(versionContract))
	}

	// 5. Write secrets.yaml
	if err := writeSecretsBundleToFile(secretsBundle); err != nil {
		return err
	}

	// 6. Generate Talos config bundle
	configBundle, err := gen.GenerateConfigBundle(
		genOptions,
		data.ClusterName,
		data.APIServerURL,
		"",
		[]string{},
		[]string{},
		[]string{},
	)
	if err != nil {
		return fmt.Errorf("failed to generate config bundle: %w", err)
	}

	// 7. Set endpoint
	configBundle.TalosConfig().Contexts[data.ClusterName].Endpoints = []string{"127.0.0.1"}

	// 8. Write talosconfig
	talosconfigFile := "talosconfig"
	tcBytes, err := yaml.Marshal(configBundle.TalosConfig())
	if err != nil {
		return fmt.Errorf("failed to marshal talosconfig: %w", err)
	}

	if err := writeToDestination(tcBytes, talosconfigFile, 0o644); err != nil {
		return err
	}

	// 9. Write preset files с подстановкой реальных значений
	log.Printf("DEBUG GenerateFromTUI: Writing preset files")
	if err := writePresetCharts(data); err != nil {
		log.Printf("DEBUG GenerateFromTUI: Error writing preset files: %v", err)
		return err
	}

	// 10. Write library chart (talm/)
	log.Printf("DEBUG GenerateFromTUI: Writing talm library chart")
	if err := writeTalmLibraryChart(); err != nil {
		log.Printf("DEBUG GenerateFromTUI: Error writing talm library chart: %v", err)
		return err
	}

	log.Printf("DEBUG GenerateFromTUI: Completed successfully")
	return nil
}

//
// ------------------- HELPERS -------------------
//

func isValidPreset(preset string) bool {
	for _, p := range generated.AvailablePresets {
		if p == preset {
			return true
		}
	}
	return false
}

func writeSecretsBundleToFile(bundle *secrets.Bundle) error {
	bundleBytes, err := yaml.Marshal(bundle)
	if err != nil {
		return fmt.Errorf("failed to marshal secrets bundle: %w", err)
	}

	return writeToDestination(bundleBytes, "secrets.yaml", 0o644)
}

func writePresetCharts(data *InitData) error {
	log.Printf("DEBUG writePresetCharts: Starting for preset %s", data.Preset)
	for path, content := range generated.PresetFiles {
		parts := strings.SplitN(path, "/", 2)
		if len(parts) < 2 {
			continue
		}

		chartName := parts[0]
		filePath := parts[1]

		log.Printf("DEBUG writePresetCharts: Processing %s, chartName=%s, filePath=%s", path, chartName, filePath)

		if chartName == data.Preset {
			log.Printf("DEBUG writePresetCharts: Matched preset, processing %s", path)
			dst := filepath.Join(filePath)
			log.Printf("DEBUG writePresetCharts: dst=%s", dst)

			if err := os.MkdirAll(filepath.Dir(dst), os.ModePerm); err != nil {
				return err
			}

			// Форматируем содержимое файлов
			formattedContent := formatFileContent(content, path, data)

			log.Printf("DEBUG writePresetCharts: Writing file to %s", dst)
			if err := writeToDestination([]byte(formattedContent), dst, 0o644); err != nil {
				log.Printf("DEBUG writePresetCharts: Error writing file: %v", err)
				return err
			}
		}
	}
	log.Printf("DEBUG writePresetCharts: Completed")
	return nil
}

func formatFileContent(content, filePath string, data *InitData) string {
	// Форматируем Chart.yaml
	if strings.HasSuffix(filePath, "Chart.yaml") {
		return fmt.Sprintf(content, data.ClusterName, "0.1.0")
	}

	// Форматируем values.yaml через парсинг YAML
	if strings.HasSuffix(filePath, "values.yaml") {
		var values map[string]interface{}
		if err := yaml.Unmarshal([]byte(content), &values); err != nil {
			// Если не удалось распарсить, возвращаем оригинальный контент
			return content
		}

		// Обновляем общие поля
		if endpoint, ok := values["endpoint"].(string); ok && (endpoint == "https://192.168.100.10:6443" || endpoint == "") {
			if data.APIServerURL != "" {
				values["endpoint"] = data.APIServerURL
			}
		}

		// Обновляем подсети
		if podSubnets, ok := values["podSubnets"].([]interface{}); ok && len(podSubnets) > 0 {
			if subnet, ok := podSubnets[0].(string); ok && (subnet == "10.244.0.0/16" || subnet == "") {
				if data.PodSubnets != "" {
					values["podSubnets"] = []string{data.PodSubnets}
				}
			}
		}

		if serviceSubnets, ok := values["serviceSubnets"].([]interface{}); ok && len(serviceSubnets) > 0 {
			if subnet, ok := serviceSubnets[0].(string); ok && (subnet == "10.96.0.0/16" || subnet == "") {
				if data.ServiceSubnets != "" {
					values["serviceSubnets"] = []string{data.ServiceSubnets}
				}
			}
		}

		if advertisedSubnets, ok := values["advertisedSubnets"].([]interface{}); ok && len(advertisedSubnets) > 0 {
			if subnet, ok := advertisedSubnets[0].(string); ok && (subnet == "192.168.100.0/24" || subnet == "") {
				if data.AdvertisedSubnets != "" {
					values["advertisedSubnets"] = []string{data.AdvertisedSubnets}
				}
			}
		}

		// Обновляем поля для cozystack
		if data.Preset == "cozystack" {
			if domain, ok := values["clusterDomain"].(string); ok && (domain == "cozy.local" || domain == "") {
				if data.ClusterDomain != "" {
					values["clusterDomain"] = data.ClusterDomain
				}
			}
			if floatingIP, ok := values["floatingIP"].(string); ok && (floatingIP == "192.168.100.10" || floatingIP == "") {
				if data.FloatingIP != "" {
					values["floatingIP"] = data.FloatingIP
				}
			}
			if image, ok := values["image"].(string); ok && (image == "ghcr.io/cozystack/cozystack/talos:v1.10.5" || image == "") {
				if data.Image != "" {
					values["image"] = data.Image
				}
			}
			if _, ok := values["oidcIssuerUrl"]; ok {
				if data.OIDCIssuerURL != "" {
					values["oidcIssuerUrl"] = data.OIDCIssuerURL
				}
			}
			if nr, ok := values["nr_hugepages"].(int); ok && nr == 0 {
				if data.NrHugepages > 0 {
					values["nr_hugepages"] = data.NrHugepages
				}
			}
		}

		// Обновляем certSANs если есть
		if certSANs, ok := values["certSANs"].([]interface{}); ok {
			if len(certSANs) == 0 {
				// Добавляем базовые certSANs
				values["certSANs"] = []string{"127.0.0.1"}
				if data.FloatingIP != "" {
					certSANsArray := values["certSANs"].([]string)
					certSANsArray = append(certSANsArray, data.FloatingIP)
					values["certSANs"] = certSANsArray
				}
			}
		}

		// Сериализуем обратно в YAML
		updatedContent, err := yaml.Marshal(values)
		if err != nil {
			return content
		}

		return string(updatedContent)
	}

	// Для других файлов возвращаем как есть
	return content
}

func writeTalmLibraryChart() error {
	for path, content := range generated.PresetFiles {
		parts := strings.SplitN(path, "/", 2)
		if len(parts) < 2 {
			continue
		}

		if parts[0] != "talm" {
			continue
		}

		filePath := parts[1]
		dst := filepath.Join("charts", "talm", filePath)

		if err := os.MkdirAll(filepath.Dir(dst), os.ModePerm); err != nil {
			return err
		}

		// Format Chart.yaml with chart name and version
		if strings.HasSuffix(path, "Chart.yaml") {
			content = fmt.Sprintf(content, "talm", "0.1.0")
		}

		if err := writeToDestination([]byte(content), dst, 0o644); err != nil {
			return err
		}
	}
	return nil
}

func writeToDestination(data []byte, destination string, permissions os.FileMode) error {
	// Check if file already exists
	if _, err := os.Stat(destination); err == nil {
		return fmt.Errorf("file %q already exists", destination)
	}

	if err := os.MkdirAll(filepath.Dir(destination), os.ModePerm); err != nil {
		return fmt.Errorf("failed to create output dir: %w", err)
	}

	err := os.WriteFile(destination, data, permissions)
	if err == nil {
		fmt.Println("Created", destination)
	}

	return err
}

// writeToDestinationNoCheck записывает файл без проверки существования (для автоинкремента)
func writeToDestinationNoCheck(data []byte, destination string, permissions os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(destination), os.ModePerm); err != nil {
		return fmt.Errorf("failed to create output dir: %w", err)
	}

	err := os.WriteFile(destination, data, permissions)
	if err == nil {
		fmt.Println("Created", destination)
	}

	return err
}

// generateNodeFileName генерирует имя файла для ноды с автоинкрементом
func generateNodeFileName(originalFilePath string) (string, error) {
	// Создаем директорию nodes/ если её нет
	nodesDir := "nodes"
	if err := os.MkdirAll(nodesDir, os.ModePerm); err != nil {
		return "", fmt.Errorf("failed to create nodes directory: %w", err)
	}

	// Сканируем существующие файлы nodes/node*.yaml
	pattern := filepath.Join(nodesDir, "node*.yaml")
	files, err := filepath.Glob(pattern)
	if err != nil {
		return "", fmt.Errorf("failed to scan existing node files: %w", err)
	}

	// Определяем максимальный номер
	maxNum := 0
	for _, file := range files {
		// Извлекаем номер из имени файла
		base := filepath.Base(file)
		if strings.HasPrefix(base, "node") && strings.HasSuffix(base, ".yaml") {
			numStr := strings.TrimPrefix(base, "node")
			numStr = strings.TrimSuffix(numStr, ".yaml")
			if numStr != "" {
				if nodeNum, err := strconv.Atoi(numStr); err == nil {
					if nodeNum > maxNum {
						maxNum = nodeNum
					}
				}
			}
		}
	}

	// Генерируем новое имя файла
	nextNum := maxNum + 1
	newFilename := fmt.Sprintf("node%d.yaml", nextNum)
	return filepath.Join(nodesDir, newFilename), nil
}
