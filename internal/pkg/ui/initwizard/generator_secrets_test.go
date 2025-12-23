package initwizard

import (
	"os"
	"testing"
	"time"

	"github.com/siderolabs/talos/pkg/machinery/config/generate/secrets"
	"github.com/stretchr/testify/assert"
)

func TestGeneratorImpl_GenerateSecretsBundle(t *testing.T) {
	generator := NewGenerator()
	
	// Создаем тестовые данные
	data := &InitData{
		ClusterName:   "test-cluster",
		TalosVersion: "v1.7.0",
	}
	
	// Генерируем secrets bundle
	err := generator.GenerateSecretsBundle(data)
	
	// Проверяем, что генерация прошла успешно
	assert.NoError(t, err, "GenerateSecretsBundle не должен возвращать ошибку")
	
	// Проверяем, что файл secrets.yaml был создан
	assert.FileExists(t, "secrets.yaml", "файл secrets.yaml должен быть создан")
	
	// Очищаем созданный файл после теста
	defer os.Remove("secrets.yaml")
}

func TestGeneratorImpl_LoadSecretsBundle(t *testing.T) {
	generator := NewGenerator()
	
	// Сначала создаем secrets bundle
	data := &InitData{
		ClusterName:   "test-cluster",
		TalosVersion: "v1.7.0",
	}
	
	err := generator.GenerateSecretsBundle(data)
	assert.NoError(t, err)
	
	// Теперь пытаемся загрузить его
	loadedBundle, err := generator.LoadSecretsBundle()
	
	// Проверяем, что загрузка прошла успешно
	assert.NoError(t, err, "LoadSecretsBundle не должен возвращать ошибку")
	assert.NotNil(t, loadedBundle, "загруженный bundle не должен быть nil")
	
	// Проверяем тип
	bundle, ok := loadedBundle.(*secrets.Bundle)
	assert.True(t, ok, "загруженный bundle должен быть типа *secrets.Bundle")
	assert.NotNil(t, bundle, "bundle не должен быть nil")
	
	// Очищаем созданный файл после теста
	defer os.Remove("secrets.yaml")
}

func TestGeneratorImpl_ValidateSecretsBundle(t *testing.T) {
	generator := NewGenerator()
	
	// Создаем secrets bundle
	data := &InitData{
		ClusterName:   "test-cluster",
		TalosVersion: "v1.7.0",
	}
	
	err := generator.GenerateSecretsBundle(data)
	assert.NoError(t, err)
	
	// Валидируем созданный bundle
	err = generator.ValidateSecretsBundle()
	
	// Проверяем, что валидация прошла успешно
	assert.NoError(t, err, "ValidateSecretsBundle не должен возвращать ошибку для корректного bundle")
	
	// Очищаем созданный файл после теста
	defer os.Remove("secrets.yaml")
}

func TestGeneratorImpl_ValidateSecretsBundle_Error(t *testing.T) {
	generator := NewGenerator()
	
	// Удаляем файл если он существует
	os.Remove("secrets.yaml")
	
	// Пытаемся валидировать несуществующий bundle
	err := generator.ValidateSecretsBundle()
	
	// Проверяем, что получена ошибка
	assert.Error(t, err, "ValidateSecretsBundle должен возвращать ошибку для несуществующего файла")
}

func TestGeneratorImpl_SaveSecretsBundle(t *testing.T) {
	generator := NewGenerator()
	
	// Создаем новый secrets bundle
	secretsBundle, err := secrets.NewBundle(secrets.NewFixedClock(time.Now()), nil)
	assert.NoError(t, err, "создание secrets bundle не должно возвращать ошибку")
	
	// Сохраняем bundle
	err = generator.SaveSecretsBundle(secretsBundle)
	
	// Проверяем, что сохранение прошло успешно
	assert.NoError(t, err, "SaveSecretsBundle не должен возвращать ошибку")
	assert.FileExists(t, "secrets.yaml", "файл secrets.yaml должен быть создан")
	
	// Очищаем созданный файл после теста
	defer os.Remove("secrets.yaml")
}

func TestGeneratorImpl_GenerateSecretsBundle_InvalidData(t *testing.T) {
	generator := NewGenerator()
	
	// Тест с nil данными
	err := generator.GenerateSecretsBundle(nil)
	assert.Error(t, err, "GenerateSecretsBundle должен возвращать ошибку для nil данных")
	
	// Тест с пустым именем кластера
	data := &InitData{
		ClusterName:   "",
	}
	
	err = generator.GenerateSecretsBundle(data)
	assert.Error(t, err, "GenerateSecretsBundle должен возвращать ошибку для пустого имени кластера")
}

func TestGeneratorImpl_GenerateSecretsBundle_InvalidTalosVersion(t *testing.T) {
	generator := NewGenerator()
	
	// Тест с некорректной версией Talos
	data := &InitData{
		ClusterName:   "test-cluster",
		TalosVersion: "invalid-version",
	}
	
	err := generator.GenerateSecretsBundle(data)
	assert.Error(t, err, "GenerateSecretsBundle должен возвращать ошибку для некорректной версии Talos")
}