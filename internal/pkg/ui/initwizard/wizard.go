package initwizard

import (
	"context"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

// WizardImpl реализует интерфейс Wizard
type WizardImpl struct {
	data      *InitData
	app       *tview.Application
	pages     *tview.Pages
	validator Validator
	scanner   NetworkScanner
	processor DataProcessor
	generator Generator
	presenter Presenter
}

// NewWizard создает новый экземпляр мастера инициализации
func NewWizard() *WizardImpl {
	// Инициализируем данные
	data := &InitData{
		Preset:      "generic",
		ClusterName: "mycluster",
	}

	// Создаем приложение
	app := tview.NewApplication()
	pages := tview.NewPages()

	// Создаем компоненты
	validator := NewValidator()
	commandExecutor := &DefaultCommandExecutor{}
	scanner := NewNetworkScanner(commandExecutor)
	processor := NewDataProcessor()
	generator := NewGenerator()

	wizard := &WizardImpl{
		data:      data,
		app:       app,
		pages:     pages,
		validator: validator,
		scanner:   scanner,
		processor: processor,
		generator: generator,
	}

	// Создаем презентер с зависимостями
	presenter := NewPresenter(app, pages, data, wizard)
	wizard.presenter = presenter

	return wizard
}

// Run запускает мастер инициализации
func (w *WizardImpl) Run() error {
	// Настраиваем логирование в файл
	logFile, err := os.OpenFile("debug.log", os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0666)
	if err != nil {
		return fmt.Errorf("не удалось открыть файл логов: %v", err)
	}
	defer logFile.Close()
	log.SetOutput(logFile)
	log.SetFlags(log.LstdFlags)
	log.SetPrefix("DEBUG: ")
	log.Printf("Запуск мастера инициализации")

	// Проверяем существующие файлы
	filesExist := w.checkExistingFiles()
	log.Printf("Проверка существующих файлов: %v", filesExist)

	// Создаем первую страницу в зависимости от состояния
	if filesExist {
		// Если файлы уже существуют, показываем мастер для добавления новой ноды
		w.presenter.ShowAddNodeWizard(w.data)
	} else {
		// Иначе показываем полный мастер
		// ShowStep1Form уже создает и добавляет страницу самостоятельно
		w.presenter.ShowStep1Form(w.data)
	}

	// Настраиваем обработку Ctrl+C
	w.setupInputCapture()

	// Запускаем приложение
	if err := w.app.SetRoot(w.pages, true).SetFocus(w.pages).Run(); err != nil {
		return fmt.Errorf("не удалось запустить приложение: %v", err)
	}

	return nil
}

// getData возвращает данные инициализации
func (w *WizardImpl) getData() *InitData {
	return w.data
}

// getApp возвращает приложение
func (w *WizardImpl) getApp() *tview.Application {
	return w.app
}

// getPages возвращает страницы
func (w *WizardImpl) getPages() *tview.Pages {
	return w.pages
}

// setupInputCapture настраивает обработку ввода
func (w *WizardImpl) setupInputCapture() {
	w.app.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		if event.Key() == tcell.KeyCtrlC {
			w.app.Stop()
			return nil
		}
		return event
	})
}

// checkExistingFiles проверяет наличие существующих файлов конфигурации
func (w *WizardImpl) checkExistingFiles() bool {
	files := []string{"Chart.yaml", "values.yaml", "secrets.yaml", "talosconfig", "kubeconfig"}
	for _, file := range files {
		if _, err := os.Stat(file); err == nil {
			return true
		}
	}
	return false
}

// PerformNetworkScan выполняет сканирование сети с прогрессом
func (w *WizardImpl) PerformNetworkScan(ctx context.Context, cidr string) ([]NodeInfo, error) {
	log.Printf("Запуск сканирования сети для CIDR: %s", cidr)

	// Валидируем CIDR
	if err := w.validator.ValidateNetworkCIDR(cidr); err != nil {
		return nil, fmt.Errorf("некорректный CIDR: %v", err)
	}

	// Создаем контекст с таймаутом
	scanCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	// Сканируем сеть с прогрессом
	nodes, err := w.scanner.ScanNetworkWithProgress(scanCtx, cidr, func(progress int) {
		log.Printf("Прогресс сканирования: %d%%", progress)
	})

	if err != nil {
		return nil, fmt.Errorf("сканирование не удалось: %v", err)
	}

	// Обрабатываем результаты сканирования
	processedNodes := w.processor.ProcessScanResults(nodes)
	
	log.Printf("Сканирование завершено, найдено %d нод", len(processedNodes))
	return processedNodes, nil
}

// ValidateAndProcessNodeConfig валидирует и обрабатывает конфигурацию ноды
func (w *WizardImpl) ValidateAndProcessNodeConfig(data *InitData) error {
	// Валидируем обязательные поля
	if err := w.validator.ValidateRequiredField(data.NodeType, "Role"); err != nil {
		return err
	}

	if err := w.validator.ValidateRequiredField(data.Hostname, "Hostname"); err != nil {
		return err
	}

	if err := w.validator.ValidateRequiredField(data.Disk, "Disk"); err != nil {
		return err
	}

	if err := w.validator.ValidateRequiredField(data.Interface, "Interface"); err != nil {
		return err
	}

	// Валидируем сетевую конфигурацию
	if err := w.validator.ValidateNetworkConfig(data.Addresses, data.Gateway, data.DNSServers); err != nil {
		return err
	}

	// Валидируем VIP если указан
	if err := w.validator.ValidateVIP(data.VIP); err != nil {
		return err
	}

	return nil
}

// GenerateAndSaveConfig генерирует и сохраняет конфигурацию
func (w *WizardImpl) GenerateAndSaveConfig(data *InitData, isFirstNode bool) error {
	log.Printf("Генерация и сохранение конфигурации, первая нода: %v", isFirstNode)

	if isFirstNode {
		// Генерируем полную конфигурацию кластера
		if err := w.generator.GenerateBootstrapConfig(data); err != nil {
			return fmt.Errorf("не удалось сгенерировать конфигурацию кластера: %v", err)
		}

		// Показываем запрос на bootstrap
		w.presenter.ShowBootstrapPrompt(data, "nodes/node1.yaml")
	} else {
		// Обновляем существующий values.yaml
		if err := w.generator.UpdateValuesYAMLWithNode(data); err != nil {
			return fmt.Errorf("не удалось обновить values.yaml: %v", err)
		}

		// Генерируем конфигурацию ноды
		nodeFileName := fmt.Sprintf("nodes/node-%d.yaml", len(w.getExistingNodes())+1)
		values, err := w.generator.LoadValuesYAML()
		if err != nil {
			return fmt.Errorf("не удалось загрузить values.yaml: %v", err)
		}

		if err := w.generator.GenerateNodeConfig(nodeFileName, data, values); err != nil {
			return fmt.Errorf("не удалось сгенерировать конфигурацию ноды: %v", err)
		}

		// Показываем успешное сообщение
		w.presenter.ShowSuccessModal(fmt.Sprintf("Нода %s успешно добавлена!\n\nКонфигурация сохранена в: %s", 
			data.Hostname, nodeFileName))
	}

	return nil
}

// BootstrapCluster выполняет bootstrap кластера
func (w *WizardImpl) BootstrapCluster() error {
	log.Printf("Запуск bootstrap кластера")

	w.presenter.ShowProgressModal("Выполняется bootstrap etcd...", func() {
		// Загружаем существующий values.yaml
		values, err := w.generator.LoadValuesYAML()
		if err != nil {
			w.presenter.ShowErrorModal(fmt.Sprintf("Не удалось загрузить values.yaml: %v", err))
			return
		}

		// Обновляем флаг etcdBootstrapped
		values.EtcdBootstrapped = true

		// Сохраняем обновленный values.yaml
		if err := w.generator.SaveValuesYAML(*values); err != nil {
			w.presenter.ShowErrorModal(fmt.Sprintf("Не удалось сохранить values.yaml: %v", err))
			return
		}

		w.presenter.ShowSuccessModal("Кластер успешно инициализирован!\n\nСледующие шаги:\n1. Проверьте файл 'kubeconfig'\n2. Используйте 'kubectl' для управления кластером")
	})

	return nil
}

// InitializeGenericCluster инициализирует generic кластер
func (w *WizardImpl) InitializeGenericCluster(data *InitData) error {
	log.Printf("Инициализация generic кластера")

	w.presenter.ShowProgressModal("Инициализация generic кластера...", func() {
		// Создаем необходимые директории
		if err := os.MkdirAll("nodes", 0755); err != nil {
			w.presenter.ShowErrorModal(fmt.Sprintf("Не удалось создать директории: %v", err))
			return
		}

		// Генерируем Chart.yaml
		chart, err := w.generator.GenerateChartYAML(data.ClusterName, data.Preset)
		if err != nil {
			w.presenter.ShowErrorModal(fmt.Sprintf("Не удалось сгенерировать Chart.yaml: %v", err))
			return
		}

		if err := w.generator.SaveChartYAML(chart); err != nil {
			w.presenter.ShowErrorModal(fmt.Sprintf("Не удалось сохранить Chart.yaml: %v", err))
			return
		}

		// Генерируем values.yaml для generic
		values, err := w.generator.GenerateValuesYAML(data)
		if err != nil {
			w.presenter.ShowErrorModal(fmt.Sprintf("Не удалось сгенерировать values.yaml: %v", err))
			return
		}

		if err := w.generator.SaveValuesYAML(values); err != nil {
			w.presenter.ShowErrorModal(fmt.Sprintf("Не удалось сохранить values.yaml: %v", err))
			return
		}

		w.presenter.ShowSuccessModal("Generic кластер успешно инициализирован!\n\nСледующие шаги:\n1. Создайте конфигурации нод в директории 'nodes/'\n2. Выполните 'talm apply' для развертывания нод")
	})

	return nil
}

// ProcessCozyStackNode обрабатывает ноду для Cozystack пресета
func (w *WizardImpl) ProcessCozyStackNode(data *InitData) error {
	log.Printf("Обработка ноды для Cozystack пресета")

	// Для Cozystack используем упрощенный workflow
	w.presenter.ShowNodeSelection(data, "Select First Control Plane Node")
	return nil
}

// getExistingNodes получает список существующих нод
func (w *WizardImpl) getExistingNodes() []NodeInfo {
	// Загружаем values.yaml для получения списка нод
	values, err := w.generator.LoadValuesYAML()
	if err != nil {
		log.Printf("Не удалось загрузить values.yaml: %v", err)
		return []NodeInfo{}
	}

	var nodes []NodeInfo
	for name, config := range values.Nodes {
		nodes = append(nodes, NodeInfo{
			Name: name,
			IP:   config.IP,
			Type: config.Type,
		})
	}

	return nodes
}

// GetValidator возвращает валидатор
func (w *WizardImpl) GetValidator() Validator {
	return w.validator
}

// GetScanner возвращает сканер сети
func (w *WizardImpl) GetScanner() NetworkScanner {
	return w.scanner
}

// GetProcessor возвращает процессор данных
func (w *WizardImpl) GetProcessor() DataProcessor {
	return w.processor
}

// GetGenerator возвращает генератор конфигураций
func (w *WizardImpl) GetGenerator() Generator {
	return w.generator
}

// GetPresenter возвращает презентер
func (w *WizardImpl) GetPresenter() Presenter {
	return w.presenter
}

// RunWithCustomConfig запускает мастер с пользовательской конфигурацией
func (w *WizardImpl) RunWithCustomConfig(config InitData) error {
	w.data = &config
	return w.Run()
}

// SetupLogging настраивает логирование
func (w *WizardImpl) SetupLogging(logFile string) error {
	file, err := os.OpenFile(logFile, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0666)
	if err != nil {
		return fmt.Errorf("не удалось открыть файл логов: %v", err)
	}
	
	log.SetOutput(file)
	log.SetFlags(log.LstdFlags)
	log.SetPrefix("DEBUG: ")
	
	return nil
}

// Shutdown корректно завершает работу мастера
func (w *WizardImpl) Shutdown() {
	log.Printf("Завершение работы мастера инициализации")
	if w.app != nil {
		w.app.Stop()
	}
}