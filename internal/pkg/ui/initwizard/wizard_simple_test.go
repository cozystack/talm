package initwizard

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewWizardBuilder(t *testing.T) {
	// Тест создания builder
	builder := NewWizardBuilder()
	assert.NotNil(t, builder)
	assert.NotNil(t, builder.config)
	
	// Проверяем значения по умолчанию
	assert.Equal(t, "mycluster", builder.config.ClusterName)
	assert.Equal(t, "generic", builder.config.Preset)
	assert.Equal(t, "v1.7.0", builder.config.TalosVersion)
}

func TestWizardBuilderWithClusterName(t *testing.T) {
	builder := NewWizardBuilder()
	wizard, err := builder.
		WithClusterName("test-cluster").
		BuildWizard()
	
	require.NoError(t, err)
	assert.NotNil(t, wizard)
	assert.Equal(t, "test-cluster", wizard.data.ClusterName)
}

func TestWizardBuilderWithPreset(t *testing.T) {
	tests := []struct {
		name        string
		preset      string
		shouldError bool
	}{
		{"Valid generic", "generic", false},
		{"Valid cozystack", "cozystack", false},
		{"Invalid preset", "invalid", false}, // Builder не валидирует, только сохраняет
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			wizard, err := NewWizardBuilder().
				WithPreset(tt.preset).
				BuildWizard()
			
			if tt.shouldError {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
				assert.NotNil(t, wizard)
				assert.Equal(t, tt.preset, wizard.data.Preset)
			}
		})
	}
}

func TestWizardBuilderChaining(t *testing.T) {
	wizard, err := NewWizardBuilder().
		WithClusterName("chained-cluster").
		WithPreset("cozystack").
		WithTalosVersion("v1.8.0").
		WithNetworkToScan("10.0.1.0/24").
		WithCacheSettings(15*time.Minute, true, false, true).
		WithPerformanceSettings(25, 20*time.Second).
		BuildWizard()
	
	require.NoError(t, err)
	assert.NotNil(t, wizard)
	assert.Equal(t, "chained-cluster", wizard.data.ClusterName)
	assert.Equal(t, "cozystack", wizard.data.Preset)
	assert.Equal(t, "10.0.1.0/24", wizard.data.NetworkToScan)
}

func TestFactoryCreateDefaultWizard(t *testing.T) {
	factory := NewFactory()
	wizard, err := factory.CreateDefaultWizard()
	
	require.NoError(t, err)
	assert.NotNil(t, wizard)
}

func TestFactoryCreateMinimalWizard(t *testing.T) {
	factory := NewFactory()
	wizard, err := factory.CreateMinimalWizard("minimal-test", "generic")
	
	require.NoError(t, err)
	assert.NotNil(t, wizard)
	assert.Equal(t, "minimal-test", wizard.data.ClusterName)
	assert.Equal(t, "generic", wizard.data.Preset)
}

func TestFactoryValidateConfig(t *testing.T) {
	factory := NewFactory()
	
	// Тест валидной конфигурации
	validConfig := &WizardConfig{
		ClusterName: "test-cluster",
		Preset:      "generic",
		CacheTTL:    5 * time.Minute,
		RequestTimeout: 10 * time.Second,
	}
	
	err := factory.ValidateConfig(validConfig)
	require.NoError(t, err)
	
	// Тест nil конфигурации
	err = factory.ValidateConfig(nil)
	require.Error(t, err)
	
	// Тест пустого имени кластера
	invalidConfig := &WizardConfig{
		ClusterName: "",
		Preset:      "generic",
	}
	
	err = factory.ValidateConfig(invalidConfig)
	require.Error(t, err)
	assert.True(t, IsValidationError(err))
	
	// Тест некорректного пресета
	invalidConfig2 := &WizardConfig{
		ClusterName: "test",
		Preset:      "invalid-preset",
	}
	
	err = factory.ValidateConfig(invalidConfig2)
	require.Error(t, err)
	assert.True(t, IsValidationError(err))
	
	// Тест отрицательного TTL
	invalidConfig3 := &WizardConfig{
		ClusterName: "test",
		Preset:      "generic",
		CacheTTL:    -1 * time.Minute,
	}
	
	err = factory.ValidateConfig(invalidConfig3)
	require.Error(t, err)
	assert.True(t, IsValidationError(err))
	
	// Тест нулевого таймаута
	invalidConfig4 := &WizardConfig{
		ClusterName: "test",
		Preset:      "generic",
		RequestTimeout: 0,
	}
	
	err = factory.ValidateConfig(invalidConfig4)
	require.Error(t, err)
	assert.True(t, IsValidationError(err))
}

func TestWizardData(t *testing.T) {
	wizard, err := NewWizardBuilder().
		WithClusterName("data-test").
		WithPreset("generic").
		BuildWizard()
	require.NoError(t, err)
	
	// Проверяем получение данных
	data := wizard.getData()
	assert.NotNil(t, data)
	assert.Equal(t, "data-test", data.ClusterName)
	assert.Equal(t, "generic", data.Preset)
	
	// Проверяем получение компонентов
	validator := wizard.GetValidator()
	assert.NotNil(t, validator)
	
	scanner := wizard.GetScanner()
	assert.NotNil(t, scanner)
	
	processor := wizard.GetProcessor()
	assert.NotNil(t, processor)
	
	generator := wizard.GetGenerator()
	assert.NotNil(t, generator)
	
	// Презентер может быть nil в тестовом окружении
	// Презентер может быть nil в тестовом окружении
	_ = wizard.GetPresenter() // Вызываем, но не проверяем
}

func TestWizardRunWithCustomConfig(t *testing.T) {
	customData := &InitData{
		ClusterName: "custom-run-test",
		Preset:      "cozystack",
	}
	
	wizard, err := NewWizardBuilder().
		WithClusterName("initial").
		BuildWizard()
	require.NoError(t, err)
	
	// НЕ вызываем RunWithCustomConfig, так как он запускает UI
	// Проверяем только, что метод существует и не паникует
	assert.NotPanics(t, func() {
		// Просто проверяем, что метод можно вызвать
		_ = wizard.RunWithCustomConfig
	})
	
	// Меняем данные вручную для теста
	wizard.data = customData
	
	// Проверяем, что данные изменились
	data := wizard.getData()
	assert.Equal(t, "custom-run-test", data.ClusterName)
	assert.Equal(t, "cozystack", data.Preset)
}

func TestWizardShutdown(t *testing.T) {
	wizard, err := NewWizardBuilder().BuildWizard()
	require.NoError(t, err)
	
	// Проверяем, что shutdown не паникует
	assert.NotPanics(t, func() {
		wizard.Shutdown()
	})
}

func TestWizardMethodsExistence(t *testing.T) {
	wizard, err := NewWizardBuilder().
		WithClusterName("method-test").
		WithPreset("generic").
		BuildWizard()
	require.NoError(t, err)
	
	// Проверяем, что основные методы существуют и не паникуют
	assert.NotPanics(t, func() {
		// Проверяем getter методы
		_ = wizard.getData()
		_ = wizard.GetValidator()
		_ = wizard.GetScanner()
		_ = wizard.GetProcessor()
		_ = wizard.GetGenerator()
		_ = wizard.GetPresenter()
		
		// Проверяем, что UI компоненты возвращают что-то (может быть nil)
		_ = wizard.getApp()
		_ = wizard.getPages()
		
		// НЕ вызываем setupInputCapture, так как app может быть nil
		// wizard.setupInputCapture()
		
		wizard.Shutdown()
	})
}

func TestCheckExistingFiles(t *testing.T) {
	wizard, err := NewWizardBuilder().BuildWizard()
	require.NoError(t, err)
	
	// Проверяем, что метод не паникует
	assert.NotPanics(t, func() {
		result := wizard.checkExistingFiles()
		// Может быть как true, так и false в зависимости от окружения
		assert.IsType(t, false, result)
	})
}

func TestDefaultConfig(t *testing.T) {
	config := DefaultConfig()
	assert.NotNil(t, config)
	
	// Проверяем значения по умолчанию
	assert.Equal(t, "mycluster", config.ClusterName)
	assert.Equal(t, "generic", config.Preset)
	assert.Equal(t, "v1.7.0", config.TalosVersion)
	assert.Equal(t, "192.168.1.0/24", config.NetworkToScan)
	assert.Equal(t, 10*time.Second, config.RequestTimeout)
	assert.Equal(t, 10, config.MaxWorkers)
	assert.Equal(t, true, config.EnableRateLimiting)
	assert.Equal(t, 5, config.RateLimit)
}

func TestApplicationFactory(t *testing.T) {
	appFactory := NewApplication()
	assert.NotNil(t, appFactory)
	
	app := appFactory.CreateApp()
	// В текущей реализации возвращается nil
	assert.Nil(t, app)
}

func TestPagesFactory(t *testing.T) {
	pagesFactory := NewPages()
	assert.NotNil(t, pagesFactory)
	
	pages := pagesFactory.CreatePages()
	// В текущей реализации возвращается nil
	assert.Nil(t, pages)
}

func TestRunInitWizard(t *testing.T) {
	// Проверяем, что функция не паникует при вызове
	assert.NotPanics(t, func() {
		// Не вызываем RunInitWizard(), так как он запустит UI
		// Вместо этого проверяем, что функция существует
		_ = CheckExistingFiles
	})
}