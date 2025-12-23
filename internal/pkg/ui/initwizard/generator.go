package initwizard

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/siderolabs/talos/pkg/machinery/config"
	"github.com/siderolabs/talos/pkg/machinery/config/generate/secrets"
	"gopkg.in/yaml.v3"
)

// GeneratorImpl реализует интерфейс Generator
type GeneratorImpl struct {
	talosVersion string
}

// NewGenerator создает новый экземпляр генератора конфигураций
func NewGenerator() Generator {
	return &GeneratorImpl{
		talosVersion: "v1.7.0",
	}
}

// GenerateChartYAML генерирует Chart.yaml для Helm чарта
func (g *GeneratorImpl) GenerateChartYAML(clusterName, preset string) (ChartYAML, error) {
	if clusterName == "" {
		return ChartYAML{}, fmt.Errorf("имя кластера не может быть пустым")
	}

	if preset == "" {
		preset = "generic"
	}

	chart := ChartYAML{
		APIVersion:  "v2",
		Name:        clusterName,
		Version:     "0.1.0",
		Description: fmt.Sprintf("Talos cluster %s", clusterName),
		Type:        "application",
		AppVersion:  g.talosVersion,
		Annotations: map[string]string{
			"preset": preset,
		},
	}

	return chart, nil
}

// GenerateValuesYAML генерирует values.yaml для Helm чарта
func (g *GeneratorImpl) GenerateValuesYAML(data *InitData) (ValuesYAML, error) {
	if data == nil {
		return ValuesYAML{}, fmt.Errorf("данные инициализации не могут быть nil")
	}

	// Определяем endpoint
	endpoint := data.APIServerURL
	if endpoint == "" {
		if data.FloatingIP != "" {
			endpoint = fmt.Sprintf("https://%s:6443", data.FloatingIP)
		} else if data.SelectedNode != "" {
			endpoint = fmt.Sprintf("https://%s:6443", data.SelectedNode)
		} else {
			return ValuesYAML{}, fmt.Errorf("не удалось определить endpoint для кластера")
		}
	}

	values := ValuesYAML{
		ClusterName:        data.ClusterName,
		FloatingIP:         data.FloatingIP,
		KubernetesEndpoint: endpoint,
		EtcdBootstrapped:   false,
		Preset:             data.Preset,
		TalosVersion:       g.talosVersion,
		Nodes:              make(map[string]NodeConfig),
	}

	// Добавляем ноды если они есть
	if len(data.DiscoveredNodes) > 0 {
		for i, node := range data.DiscoveredNodes {
			nodeName := fmt.Sprintf("node-%d", i+1)
			values.Nodes[nodeName] = NodeConfig{
				Type: node.Type,
				IP:   node.IP,
			}
		}
	}

	return values, nil
}

// GenerateMachineConfig генерирует конфигурацию машины
func (g *GeneratorImpl) GenerateMachineConfig(data *InitData) (string, error) {
	if data == nil {
		return "", fmt.Errorf("данные инициализации не могут быть nil")
	}

	if data.NodeType == "" {
		return "", fmt.Errorf("тип ноды не может быть пустым")
	}

	if data.Hostname == "" {
		return "", fmt.Errorf("имя хоста не может быть пустым")
	}

	if data.Disk == "" {
		return "", fmt.Errorf("диск не может быть пустым")
	}

	config := fmt.Sprintf(`machine:
  type: %s
  install:
    disk: /dev/%s
  network:
    hostname: %s
    nameservers: [%s]
    interfaces:
    - interface: %s
      addresses: [%s]
      routes:
        - network: 0.0.0.0/0
          gateway: %s`, data.NodeType, data.Disk, data.Hostname, data.DNSServers, data.Interface, data.Addresses, data.Gateway)

	if data.VIP != "" {
		config += fmt.Sprintf(`
      vip:
        ip: %s`, data.VIP)
	}

	return config, nil
}

// GenerateNodeConfig генерирует конфигурацию для ноды
func (g *GeneratorImpl) GenerateNodeConfig(filename string, data *InitData, values *ValuesYAML) error {
	if filename == "" {
		return fmt.Errorf("имя файла не может быть пустым")
	}

	if data == nil {
		return fmt.Errorf("данные инициализации не могут быть nil")
	}

	if values == nil {
		return fmt.Errorf("значения не могут быть nil")
	}

	config := fmt.Sprintf(`# Конфигурация ноды %s
node:
  type: %s
  ip: %s
cluster:
  name: %s
  endpoint: %s
`, filepath.Base(filename), data.NodeType, data.SelectedNode,
		data.ClusterName, values.KubernetesEndpoint)

	return os.WriteFile(filename, []byte(config), 0644)
}

// SaveChartYAML сохраняет Chart.yaml в файл
func (g *GeneratorImpl) SaveChartYAML(chart ChartYAML) error {
	data, err := yaml.Marshal(chart)
	if err != nil {
		return fmt.Errorf("не удалось сериализовать Chart.yaml: %v", err)
	}

	return os.WriteFile("Chart.yaml", data, 0644)
}

// SaveValuesYAML сохраняет values.yaml в файл
func (g *GeneratorImpl) SaveValuesYAML(values ValuesYAML) error {
	data, err := yaml.Marshal(values)
	if err != nil {
		return fmt.Errorf("не удалось сериализовать values.yaml: %v", err)
	}

	return os.WriteFile("values.yaml", data, 0644)
}

// LoadValuesYAML загружает существующий values.yaml
func (g *GeneratorImpl) LoadValuesYAML() (*ValuesYAML, error) {
	data, err := os.ReadFile("values.yaml")
	if err != nil {
		return nil, fmt.Errorf("не удалось прочитать values.yaml: %v", err)
	}

	var values ValuesYAML
	if err := yaml.Unmarshal(data, &values); err != nil {
		return nil, fmt.Errorf("не удалось десериализовать values.yaml: %v", err)
	}

	return &values, nil
}

// GeneratePresetSpecificConfig генерирует конфигурацию специфичную для пресета
func (g *GeneratorImpl) GeneratePresetSpecificConfig(preset string, data *InitData) (map[string]interface{}, error) {
	config := make(map[string]interface{})

	switch preset {
	case "generic":
		config = g.generateGenericConfig(data)
	case "cozystack":
		config = g.generateCozystackConfig(data)
	default:
		return nil, fmt.Errorf("неподдерживаемый пресет: %s", preset)
	}

	return config, nil
}

// generateGenericConfig генерирует конфигурацию для generic пресета
func (g *GeneratorImpl) generateGenericConfig(data *InitData) map[string]interface{} {
	config := map[string]interface{}{
		"clusterName":        data.ClusterName,
		"kubernetesEndpoint": data.APIServerURL,
		"floatingIP":         data.FloatingIP,
		"preset":             "generic",
		"talosVersion":       g.talosVersion,
		"etcdBootstrapped":   false,
	}

	// Добавляем опциональные поля если они есть
	if data.PodSubnets != "" {
		config["podSubnets"] = data.PodSubnets
	}

	if data.ServiceSubnets != "" {
		config["serviceSubnets"] = data.ServiceSubnets
	}

	if data.AdvertisedSubnets != "" {
		config["advertisedSubnets"] = data.AdvertisedSubnets
	}

	if data.ClusterDomain != "" {
		config["clusterDomain"] = data.ClusterDomain
	}

	if data.Image != "" {
		config["image"] = data.Image
	}

	if data.OIDCIssuerURL != "" {
		config["oidcIssuerURL"] = data.OIDCIssuerURL
	}

	if data.NrHugepages > 0 {
		config["nrHugepages"] = data.NrHugepages
	}

	return config
}

// generateCozystackConfig генерирует конфигурацию для cozystack пресета
func (g *GeneratorImpl) generateCozystackConfig(data *InitData) map[string]interface{} {
	config := map[string]interface{}{
		"clusterName":        data.ClusterName,
		"floatingIP":         data.FloatingIP,
		"preset":             "cozystack",
		"talosVersion":       g.talosVersion,
		"etcdBootstrapped":   false,
	}

	// Определяем endpoint для cozystack
	if data.FloatingIP != "" {
		config["kubernetesEndpoint"] = fmt.Sprintf("https://%s:6443", data.FloatingIP)
	} else if data.SelectedNode != "" {
		config["kubernetesEndpoint"] = fmt.Sprintf("https://%s:6443", data.SelectedNode)
	}

	// Добавляем ноды если они есть
	if len(data.DiscoveredNodes) > 0 {
		nodes := make(map[string]NodeConfig)
		for i, node := range data.DiscoveredNodes {
			nodeName := fmt.Sprintf("node-%d", i+1)
			nodes[nodeName] = NodeConfig{
				Type: node.Type,
				IP:   node.IP,
			}
		}
		config["nodes"] = nodes
	}

	return config
}

// UpdateValuesYAMLWithNode обновляет values.yaml добавлением новой ноды
func (g *GeneratorImpl) UpdateValuesYAMLWithNode(data *InitData) error {
	// Загружаем существующий values.yaml
	values, err := g.LoadValuesYAML()
	if err != nil {
		return fmt.Errorf("не удалось загрузить values.yaml: %v", err)
	}

	// Генерируем имя для новой ноды
	nodeName := fmt.Sprintf("node-%d", len(values.Nodes)+1)
	if values.Nodes == nil {
		values.Nodes = make(map[string]NodeConfig)
	}

	// Добавляем новую ноду
	values.Nodes[nodeName] = NodeConfig{
		Type: data.NodeType,
		IP:   data.SelectedNode,
	}

	// Обновляем clusterName и preset если они изменились
	if data.ClusterName != "" {
		values.ClusterName = data.ClusterName
	}
	if data.Preset != "" {
		values.Preset = data.Preset
	}
	if data.FloatingIP != "" {
		values.FloatingIP = data.FloatingIP
	}

	// Сохраняем обновленный values.yaml
	return g.SaveValuesYAML(*values)
}

// GenerateBootstrapConfig генерирует конфигурацию для bootstrap
func (g *GeneratorImpl) GenerateBootstrapConfig(data *InitData) error {
	if data == nil {
		return fmt.Errorf("данные инициализации не могут быть nil")
	}

	// Создаем необходимые директории
	if err := os.MkdirAll("nodes", 0755); err != nil {
		return fmt.Errorf("не удалось создать директории: %v", err)
	}

	// Генерируем Chart.yaml
	chart, err := g.GenerateChartYAML(data.ClusterName, data.Preset)
	if err != nil {
		return fmt.Errorf("не удалось сгенерировать Chart.yaml: %v", err)
	}

	if err := g.SaveChartYAML(chart); err != nil {
		return fmt.Errorf("не удалось сохранить Chart.yaml: %v", err)
	}

	// Генерируем values.yaml
	values, err := g.GenerateValuesYAML(data)
	if err != nil {
		return fmt.Errorf("не удалось сгенерировать values.yaml: %v", err)
	}

	if err := g.SaveValuesYAML(values); err != nil {
		return fmt.Errorf("не удалось сохранить values.yaml: %v", err)
	}

	// Генерируем конфигурацию для первой ноды
	nodeFileName := "nodes/node1.yaml"
	if err := g.GenerateNodeConfig(nodeFileName, data, &values); err != nil {
		return fmt.Errorf("не удалось сгенерировать конфигурацию ноды: %v", err)
	}

	// Генерируем secrets bundle для кластера
	if err := g.GenerateSecretsBundle(data); err != nil {
		return fmt.Errorf("не удалось сгенерировать secrets bundle: %v", err)
	}

	return nil
}

// ValidateGeneratedConfig валидирует сгенерированную конфигурацию
func (g *GeneratorImpl) ValidateGeneratedConfig(chart ChartYAML, values ValuesYAML) error {
	// Проверяем Chart.yaml
	if chart.Name == "" {
		return fmt.Errorf("имя в Chart.yaml не может быть пустым")
	}

	if chart.APIVersion == "" {
		return fmt.Errorf("версия API в Chart.yaml не может быть пустой")
	}

	// Проверяем values.yaml
	if values.ClusterName == "" {
		return fmt.Errorf("имя кластера в values.yaml не может быть пустым")
	}

	if values.KubernetesEndpoint == "" {
		return fmt.Errorf("endpoint Kubernetes в values.yaml не может быть пустым")
	}

	if values.TalosVersion == "" {
		return fmt.Errorf("версия Talos в values.yaml не может быть пустой")
	}

	return nil
}

// GenerateSecretsBundle генерирует secrets bundle для кластера
func (g *GeneratorImpl) GenerateSecretsBundle(data *InitData) error {
	if data == nil {
		return fmt.Errorf("данные инициализации не могут быть nil")
	}

	if data.ClusterName == "" {
		return fmt.Errorf("имя кластера не может быть пустым")
	}

	// Определяем версию Talos для контракта
	var versionContract *config.VersionContract
	var err error

	if data.TalosVersion != "" {
		versionContract, err = config.ParseContractFromVersion(data.TalosVersion)
		if err != nil {
			return fmt.Errorf("некорректная версия Talos: %w", err)
		}
	}

	// Создаем secrets bundle
	secretsBundle, err := secrets.NewBundle(secrets.NewFixedClock(time.Now()), versionContract)
	if err != nil {
		return fmt.Errorf("не удалось создать secrets bundle: %w", err)
	}

	// Сохраняем bundle в файл
	if err := g.SaveSecretsBundle(secretsBundle); err != nil {
		return fmt.Errorf("не удалось сохранить secrets bundle: %w", err)
	}

	return nil
}

// LoadSecretsBundle загружает существующий secrets bundle
func (g *GeneratorImpl) LoadSecretsBundle() (interface{}, error) {
	data, err := os.ReadFile("secrets.yaml")
	if err != nil {
		return nil, fmt.Errorf("не удалось прочитать secrets.yaml: %v", err)
	}

	var secretsBundle secrets.Bundle
	if err := yaml.Unmarshal(data, &secretsBundle); err != nil {
		return nil, fmt.Errorf("не удалось десериализовать secrets bundle: %v", err)
	}

	return &secretsBundle, nil
}

// ValidateSecretsBundle валидирует secrets bundle
func (g *GeneratorImpl) ValidateSecretsBundle() error {
	secretsBundle, err := g.LoadSecretsBundle()
	if err != nil {
		return fmt.Errorf("не удалось загрузить secrets bundle: %w", err)
	}

	bundle, ok := secretsBundle.(*secrets.Bundle)
	if !ok {
		return fmt.Errorf("некорректный тип secrets bundle")
	}

	// Проверяем обязательные компоненты secrets bundle
	if bundle.Certs == nil {
		return fmt.Errorf("сертификаты отсутствуют в secrets bundle")
	}

	if bundle.Certs.OS == nil {
		return fmt.Errorf("Talos OS сертификаты отсутствуют в secrets bundle")
	}

	if bundle.Certs.OS.Crt == nil {
		return fmt.Errorf("OS сертификат отсутствует в secrets bundle")
	}

	return nil
}

// SaveSecretsBundle сохраняет secrets bundle в файл
func (g *GeneratorImpl) SaveSecretsBundle(bundle *secrets.Bundle) error {
	data, err := yaml.Marshal(bundle)
	if err != nil {
		return fmt.Errorf("не удалось сериализовать secrets bundle: %v", err)
	}

	return os.WriteFile("secrets.yaml", data, 0644)
}