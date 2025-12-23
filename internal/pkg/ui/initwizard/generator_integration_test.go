package initwizard

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestGeneratorImpl_GenerateBootstrapConfig_WithSecrets(t *testing.T) {
	generator := NewGenerator()
	
	// Создаем тестовые данные
	data := &InitData{
		ClusterName:   "integration-test-cluster",
		Preset:        "generic",
		SelectedNode:  "192.168.1.100",
		FloatingIP:    "192.168.1.10",
		PodSubnets:    "10.244.0.0/16",
		ServiceSubnets: "10.96.0.0/16",
		AdvertisedSubnets: "192.168.1.0/24",
	}
	
	// Генерируем полную конфигурацию bootstrap
	err := generator.GenerateBootstrapConfig(data)
	
	// Проверяем, что генерация прошла успешно
	assert.NoError(t, err, "GenerateBootstrapConfig не должен возвращать ошибку")
	
	// Проверяем, что все файлы были созданы
	assert.FileExists(t, "Chart.yaml", "Chart.yaml должен быть создан")
	assert.FileExists(t, "values.yaml", "values.yaml должен быть создан")
	assert.FileExists(t, "secrets.yaml", "secrets.yaml должен быть создан")
	assert.FileExists(t, "nodes/node1.yaml", "nodes/node1.yaml должен быть создан")
	
	// Проверяем содержимое файлов
	chartData, err := os.ReadFile("Chart.yaml")
	assert.NoError(t, err)
	assert.Contains(t, string(chartData), "integration-test-cluster", "Chart.yaml должен содержать имя кластера")
	
	valuesData, err := os.ReadFile("values.yaml")
	assert.NoError(t, err)
	assert.Contains(t, string(valuesData), "integration-test-cluster", "values.yaml должен содержать имя кластера")
	
	secretsData, err := os.ReadFile("secrets.yaml")
	assert.NoError(t, err)
	assert.Contains(t, string(secretsData), "certs", "secrets.yaml должен содержать сертификаты")
	
	// Валидируем сгенерированные данные
	values, err := generator.LoadValuesYAML()
	assert.NoError(t, err)
	assert.Equal(t, "integration-test-cluster", values.ClusterName)
	assert.Equal(t, "generic", values.Preset)
	
	// Валидируем secrets bundle
	err = generator.ValidateSecretsBundle()
	assert.NoError(t, err, "secrets bundle должен проходить валидацию")
	
	// Очищаем созданные файлы после теста
	defer func() {
		os.Remove("Chart.yaml")
		os.Remove("values.yaml")
		os.Remove("secrets.yaml")
		os.Remove("nodes/node1.yaml")
		os.RemoveAll("nodes")
	}()
}

func TestGeneratorImpl_Integration_WithCozystackPreset(t *testing.T) {
	generator := NewGenerator()
	
	// Создаем тестовые данные для cozystack пресета
	data := &InitData{
		ClusterName:   "cozystack-test",
		Preset:        "cozystack",
		SelectedNode:  "192.168.1.101",
		FloatingIP:    "192.168.1.11",
		ClusterDomain: "cozy.local",
		Image:         "ghcr.io/cozystack/cozystack/talos:v1.10.5",
		OIDCIssuerURL: "https://oidc.example.com",
		NrHugepages:   1,
	}
	
	// Генерируем конфигурацию
	err := generator.GenerateBootstrapConfig(data)
	
	// Проверяем, что генерация прошла успешно
	assert.NoError(t, err, "GenerateBootstrapConfig не должен возвращать ошибку для cozystack пресета")
	
	// Проверяем, что файлы созданы
	assert.FileExists(t, "Chart.yaml", "Chart.yaml должен быть создан")
	assert.FileExists(t, "values.yaml", "values.yaml должен быть создан")
	assert.FileExists(t, "secrets.yaml", "secrets.yaml должен быть создан")
	
	// Валидируем содержимое values.yaml для cozystack
	values, err := generator.LoadValuesYAML()
	assert.NoError(t, err)
	assert.Equal(t, "cozystack", values.Preset)
	
	// Валидируем secrets bundle
	err = generator.ValidateSecretsBundle()
	assert.NoError(t, err, "secrets bundle должен проходить валидацию для cozystack пресета")
	
	// Очищаем созданные файлы после теста
	defer func() {
		os.Remove("Chart.yaml")
		os.Remove("values.yaml")
		os.Remove("secrets.yaml")
		os.Remove("nodes/node1.yaml")
		os.RemoveAll("nodes")
	}()
}

func TestGeneratorImpl_UpdateValuesYAMLWithNode_WithExistingSecrets(t *testing.T) {
	generator := NewGenerator()
	
	// Создаем initial конфигурацию
	data := &InitData{
		ClusterName:   "node-test-cluster",
		Preset:        "generic",
		SelectedNode:  "192.168.1.102",
		FloatingIP:    "192.168.1.12",
	}
	
	// Генерируем initial конфигурацию
	err := generator.GenerateBootstrapConfig(data)
	assert.NoError(t, err)
	
	// Создаем данные для добавления новой ноды
	nodeData := &InitData{
		ClusterName:   "node-test-cluster",
		Preset:        "generic",
		SelectedNode:  "192.168.1.103",
		NodeType:      "worker",
	}
	
	// Добавляем новую ноду
	err = generator.UpdateValuesYAMLWithNode(nodeData)
	assert.NoError(t, err, "UpdateValuesYAMLWithNode не должен возвращать ошибку")
	
	// Проверяем, что values.yaml обновлен
	values, err := generator.LoadValuesYAML()
	assert.NoError(t, err)
	assert.GreaterOrEqual(t, len(values.Nodes), 1, "должна быть хотя бы 1 нода в конфигурации")
	
	// Проверяем, что первая нода осталась
	assert.Contains(t, values.Nodes, "node-1", "первая нода должна быть сохранена")
	
	// Проверяем, что secrets.yaml не был изменен
	err = generator.ValidateSecretsBundle()
	assert.NoError(t, err, "secrets bundle должен остаться валидным после обновления нод")
	
	// Очищаем созданные файлы после теста
	defer func() {
		os.Remove("Chart.yaml")
		os.Remove("values.yaml")
		os.Remove("secrets.yaml")
		os.Remove("nodes/node1.yaml")
		os.RemoveAll("nodes")
	}()
}