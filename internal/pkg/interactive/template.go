package interactive

import (
	"context"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"strings"

	"github.com/cozystack/talm/pkg/engine"
	"github.com/cozystack/talm/pkg/modeline"
)

// TemplateManager менеджер для работы с шаблонами
type TemplateManager struct {
	rootDir       string
	templateFiles []string
	valuesFiles   []string
	outputFiles   []string
}

// NewTemplateManager создает новый менеджер шаблонов
func NewTemplateManager(rootDir string) *TemplateManager {
	return &TemplateManager{
		rootDir:       rootDir,
		templateFiles: []string{},
		valuesFiles:   []string{},
		outputFiles:   []string{},
	}
}

// DiscoverTemplates обнаруживает доступные шаблоны
func (tm *TemplateManager) DiscoverTemplates() error {
	templates := []string{}
	values := []string{}

	// Ищем шаблоны в директории templates/
	templatesDir := filepath.Join(tm.rootDir, "templates")
	if _, err := os.Stat(templatesDir); err == nil {
		files, err := ioutil.ReadDir(templatesDir)
		if err != nil {
			return fmt.Errorf("не удалось прочитать директорию templates: %v", err)
		}

		for _, file := range files {
			if strings.HasSuffix(file.Name(), ".yaml") {
				templates = append(templates, fmt.Sprintf("templates/%s", file.Name()))
			}
		}
	}

	// Ищем шаблоны в charts/
	chartsDir := filepath.Join(tm.rootDir, "charts")
	if _, err := os.Stat(chartsDir); err == nil {
		// Рекурсивно ищем во всех chart'ах
		err := filepath.Walk(chartsDir, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return err
			}
			if strings.Contains(path, "templates") && strings.HasSuffix(path, ".yaml") {
				relPath, _ := filepath.Rel(tm.rootDir, path)
				templates = append(templates, relPath)
			}
			return nil
		})
		if err != nil {
			return fmt.Errorf("ошибка при сканировании charts: %v", err)
		}
	}

	// Ищем файлы значений
	if _, err := os.Stat(filepath.Join(tm.rootDir, "values.yaml")); err == nil {
		values = append(values, "values.yaml")
	}

	// Ищем дополнительные файлы значений
	valuesDir := filepath.Join(tm.rootDir, "values")
	if _, err := os.Stat(valuesDir); err == nil {
		files, err := ioutil.ReadDir(valuesDir)
		if err != nil {
			return fmt.Errorf("не удалось прочитать директорию values: %v", err)
		}

		for _, file := range files {
			if strings.HasSuffix(file.Name(), ".yaml") || strings.HasSuffix(file.Name(), ".yml") {
				values = append(values, filepath.Join("values", file.Name()))
			}
		}
	}

	tm.templateFiles = templates
	tm.valuesFiles = values

	return nil
}

// GetTemplateFiles возвращает список файлов шаблонов
func (tm *TemplateManager) GetTemplateFiles() []string {
	return tm.templateFiles
}

// GetValuesFiles возвращает список файлов значений
func (tm *TemplateManager) GetValuesFiles() []string {
	return tm.valuesFiles
}

// RenderTemplates рендерит выбранные шаблоны
func (tm *TemplateManager) RenderTemplates(ctx context.Context, selectedTemplates []string, customValues map[string]string) (map[string]string, error) {
	// Настраиваем опции для рендеринга
	opts := engine.Options{
		Root:       tm.rootDir,
		Offline:    true, // Работаем в offline режиме для интерактивного режима
		Debug:      false,
		Full:       true,
		TemplateFiles: selectedTemplates,
	}

	// Добавляем кастомные значения
	for key, value := range customValues {
		opts.Values = append(opts.Values, fmt.Sprintf("%s=%s", key, value))
	}

	// Добавляем файлы значений
	opts.ValueFiles = tm.valuesFiles

	// Выполняем рендеринг
	result, err := engine.Render(ctx, nil, opts)
	if err != nil {
		return nil, fmt.Errorf("ошибка рендеринга шаблонов: %v", err)
	}

	// Конвертируем []byte в map[string]string
	resultMap := make(map[string]string)
	for key, value := range result {
		resultMap[string(key)] = string(value)
	}

	return resultMap, nil
}

// SaveRenderedTemplates сохраняет отрендеренные шаблоны в файлы
func (tm *TemplateManager) SaveRenderedTemplates(result map[string]string, outputDir string) error {
	if outputDir == "" {
		outputDir = filepath.Join(tm.rootDir, "rendered")
	}

	// Создаем выходную директорию
	if err := os.MkdirAll(outputDir, 0755); err != nil {
		return fmt.Errorf("не удалось создать выходную директорию: %v", err)
	}

	for templatePath, content := range result {
		// Определяем имя выходного файла
		outputFile := filepath.Base(templatePath)
		if strings.Contains(templatePath, "templates/") {
			// Извлекаем относительный путь от templates/
			relPath := strings.TrimPrefix(templatePath, "templates/")
			outputFile = relPath
		}

		outputPath := filepath.Join(outputDir, outputFile)

		// Сохраняем файл
		if err := ioutil.WriteFile(outputPath, []byte(content), 0644); err != nil {
			return fmt.Errorf("не удалось сохранить файл %s: %v", outputPath, err)
		}

		tm.outputFiles = append(tm.outputFiles, outputPath)
	}

	return nil
}

// GenerateModeline генерирует modeline для выбранных шаблонов
func (tm *TemplateManager) GenerateModeline(selectedTemplates []string, nodes, endpoints []string) (string, error) {
	return modeline.GenerateModeline(nodes, endpoints, selectedTemplates)
}

// LoadModelineFromFile загружает modeline из файла
func (tm *TemplateManager) LoadModelineFromFile(filename string) (*modeline.Config, error) {
	return modeline.ReadAndParseModeline(filename)
}

// GetPresetTemplates возвращает шаблоны для заданного пресета
func (tm *TemplateManager) GetPresetTemplates(preset string) []string {
	var presetTemplates []string

	switch preset {
	case "generic":
		presetTemplates = []string{
			"generic/templates/controlplane.yaml",
			"generic/templates/worker.yaml",
		}
	case "cozystack":
		presetTemplates = []string{
			"cozystack/templates/controlplane.yaml",
			"cozystack/templates/worker.yaml",
		}
	default:
		presetTemplates = tm.templateFiles
	}

	return presetTemplates
}

// GetOutputFiles возвращает список выходных файлов
func (tm *TemplateManager) GetOutputFiles() []string {
	return tm.outputFiles
}

// ClearOutputFiles очищает список выходных файлов
func (tm *TemplateManager) ClearOutputFiles() {
	tm.outputFiles = []string{}
}