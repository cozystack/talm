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
	// Initialize data
	data := &InitData{
		Preset:        "generic",
		ClusterName:   "mycluster",
		NetworkToScan: "192.168.1.0/24", // Значение по умолчанию для сканирования сети
	}

	// Create application
	app := tview.NewApplication()
	pages := tview.NewPages()

	// Create components
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

	// Create presenter with dependencies
	presenter := NewPresenter(app, pages, data, wizard)
	wizard.presenter = presenter

	return wizard
}

// Run starts the initialization wizard
func (w *WizardImpl) Run() error {
	// Configure logging to file
	logFile, err := os.OpenFile("debug.log", os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0666)
	if err != nil {
		return fmt.Errorf("failed to open log file: %v", err)
	}
	defer logFile.Close()
	log.SetOutput(logFile)
	log.SetFlags(log.LstdFlags)
	log.SetPrefix("DEBUG: ")
	log.Printf("Starting initialization wizard")

	// Check existing files
	filesExist := w.checkExistingFiles()
	log.Printf("Checking existing files: %v", filesExist)

	// Create first page depending on state
	if filesExist {
		// If files already exist, show wizard for adding new node
		log.Printf("Diagnostics: calling ShowAddNodeWizard, filesExist=%v", filesExist)
		w.presenter.ShowAddNodeWizard(w.data)
	} else {
		// Иначе показываем полный мастер
		log.Printf("Diagnostics: calling ShowStep1Form, filesExist=%v", filesExist)
		log.Printf("Diagnostics: w.presenter=%v, w.data=%v", w.presenter, w.data)
		
		// Check presenter state
		if w.presenter == nil {
			log.Printf("CRITICAL ERROR: w.presenter is nil!")
			return fmt.Errorf("presenter not initialized")
		}
		
		// Check data state
		if w.data == nil {
			log.Printf("CRITICAL ERROR: w.data is nil!")
			return fmt.Errorf("initialization data not initialized")
		}
		
		log.Printf("Diagnostics: presenter and data are fine, calling ShowStep1Form")
		// ShowStep1Form already creates and adds the page itself
		w.presenter.ShowStep1Form(w.data)
	}

	// Configure Ctrl+C handling
	w.setupInputCapture()

	// Start application
	log.Printf("WIZARD DIAGNOSTICS: Before app.SetRoot...")
	if err := w.app.SetRoot(w.pages, true).SetFocus(w.pages).Run(); err != nil {
		log.Printf("WIZARD DIAGNOSTICS: Application startup error: %v", err)
		return fmt.Errorf("failed to start application: %v", err)
	}
	log.Printf("WIZARD DIAGNOSTICS: Application finished")

	return nil
}

// getData returns initialization data
func (w *WizardImpl) getData() *InitData {
	return w.data
}

// getApp returns application
func (w *WizardImpl) getApp() *tview.Application {
	return w.app
}

// getPages returns pages
func (w *WizardImpl) getPages() *tview.Pages {
	return w.pages
}

// setupInputCapture configures input handling
func (w *WizardImpl) setupInputCapture() {
	w.app.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		if event.Key() == tcell.KeyCtrlC {
			w.app.Stop()
			return nil
		}
		return event
	})
}

// checkExistingFiles checks for existing configuration files
func (w *WizardImpl) checkExistingFiles() bool {
	files := []string{"Chart.yaml", "values.yaml", "secrets.yaml", "talosconfig", "kubeconfig"}
	for _, file := range files {
		if _, err := os.Stat(file); err == nil {
			return true
		}
	}
	return false
}

// PerformNetworkScan performs network scanning with progress
func (w *WizardImpl) PerformNetworkScan(ctx context.Context, cidr string) ([]NodeInfo, error) {
	log.Printf("Starting network scan for CIDR: %s", cidr)

	// Validate CIDR
	if err := w.validator.ValidateNetworkCIDR(cidr); err != nil {
		return nil, fmt.Errorf("incorrect CIDR: %v", err)
	}

	// Create context with timeout
	scanCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	// Scan network with progress
	nodes, err := w.scanner.ScanNetworkWithProgress(scanCtx, cidr, func(progress int) {
		log.Printf("Scan progress: %d%%", progress)
	})

	if err != nil {
		return nil, fmt.Errorf("scanning failed: %v", err)
	}

	// Process scan results
	processedNodes := w.processor.ProcessScanResults(nodes)
	
	log.Printf("Scanning completed, found %d nodes", len(processedNodes))
	return processedNodes, nil
}

// ValidateAndProcessNodeConfig validates and processes node configuration
func (w *WizardImpl) ValidateAndProcessNodeConfig(data *InitData) error {
	// Validate required fields
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

	// Validate network configuration
	if err := w.validator.ValidateNetworkConfig(data.Addresses, data.Gateway, data.DNSServers); err != nil {
		return err
	}

	// Validate VIP if specified
	if err := w.validator.ValidateVIP(data.VIP); err != nil {
		return err
	}

	return nil
}

// GenerateAndSaveConfig generates and saves configuration
func (w *WizardImpl) GenerateAndSaveConfig(data *InitData, isFirstNode bool) error {
	log.Printf("Configuration generation and saving, first node: %v", isFirstNode)

	if isFirstNode {
		// Generate full cluster configuration
		if err := w.generator.GenerateBootstrapConfig(data); err != nil {
			return fmt.Errorf("failed to generate cluster configuration: %v", err)
		}

		// Show bootstrap prompt
		w.presenter.ShowBootstrapPrompt(data, "nodes/node1.yaml")
	} else {
		// Update existing values.yaml
		if err := w.generator.UpdateValuesYAMLWithNode(data); err != nil {
			return fmt.Errorf("failed to update values.yaml: %v", err)
		}

		// Generate node configuration
		nodeFileName := fmt.Sprintf("nodes/node-%d.yaml", len(w.getExistingNodes())+1)
		values, err := w.generator.LoadValuesYAML()
		if err != nil {
			return fmt.Errorf("не удалось загрузить values.yaml: %v", err)
		}

		if err := w.generator.GenerateNodeConfig(nodeFileName, data, values); err != nil {
			return fmt.Errorf("failed to generate node configuration: %v", err)
		}

		// Show success message
		w.presenter.ShowSuccessModal(fmt.Sprintf("Нода %s успешно добавлена!\n\nКонфигурация сохранена в: %s", 
			data.Hostname, nodeFileName))
	}

	return nil
}

// BootstrapCluster performs cluster bootstrap
func (w *WizardImpl) BootstrapCluster() error {
	log.Printf("Starting cluster bootstrap")

	w.presenter.ShowProgressModal("Выполняется bootstrap etcd...", func() {
		// Load existing values.yaml
		values, err := w.generator.LoadValuesYAML()
		if err != nil {
			w.presenter.ShowErrorModal(fmt.Sprintf("Failed to load values.yaml: %v", err))
			return
		}

		// Update etcdBootstrapped flag
		values.EtcdBootstrapped = true

		// Save updated values.yaml
		if err := w.generator.SaveValuesYAML(*values); err != nil {
			w.presenter.ShowErrorModal(fmt.Sprintf("Failed to save values.yaml: %v", err))
			return
		}

		w.presenter.ShowSuccessModal("Кластер успешно инициализирован!\n\nСледующие шаги:\n1. Проверьте файл 'kubeconfig'\n2. Используйте 'kubectl' для управления кластером")
	})

	return nil
}

// InitializeGenericCluster initializes generic cluster
func (w *WizardImpl) InitializeGenericCluster(data *InitData) error {
	log.Printf("Generic cluster initialization")

	w.presenter.ShowProgressModal("Generic cluster initialization...", func() {
		// Create necessary directories
		if err := os.MkdirAll("nodes", 0755); err != nil {
			w.presenter.ShowErrorModal(fmt.Sprintf("Failed to create directories: %v", err))
			return
		}

		// Generate Chart.yaml
		chart, err := w.generator.GenerateChartYAML(data.ClusterName, data.Preset)
		if err != nil {
			w.presenter.ShowErrorModal(fmt.Sprintf("Failed to generate Chart.yaml: %v", err))
			return
		}

		if err := w.generator.SaveChartYAML(chart); err != nil {
			w.presenter.ShowErrorModal(fmt.Sprintf("Failed to save Chart.yaml: %v", err))
			return
		}

		// Генерируем values.yaml для generic
		values, err := w.generator.GenerateValuesYAML(data)
		if err != nil {
			w.presenter.ShowErrorModal(fmt.Sprintf("Не удалось сгенерировать values.yaml: %v", err))
			return
		}

		if err := w.generator.SaveValuesYAML(values); err != nil {
			w.presenter.ShowErrorModal(fmt.Sprintf("Failed to save values.yaml: %v", err))
			return
		}

		w.presenter.ShowSuccessModal("Generic кластер успешно инициализирован!\n\nСледующие шаги:\n1. Создайте конфигурации нод в директории 'nodes/'\n2. Выполните 'talm apply' для развертывания нод")
	})

	return nil
}

// ProcessCozyStackNode processes node for Cozystack preset
func (w *WizardImpl) ProcessCozyStackNode(data *InitData) error {
	log.Printf("Processing node for Cozystack preset")

	// For Cozystack use simplified workflow
	w.presenter.ShowNodeSelection(data, "Select First Control Plane Node")
	return nil
}

// getExistingNodes gets list of existing nodes
func (w *WizardImpl) getExistingNodes() []NodeInfo {
	// Load values.yaml to get list of nodes
	values, err := w.generator.LoadValuesYAML()
	if err != nil {
		log.Printf("Failed to load values.yaml: %v", err)
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

// GetValidator returns validator
func (w *WizardImpl) GetValidator() Validator {
	return w.validator
}

// GetScanner returns network scanner
func (w *WizardImpl) GetScanner() NetworkScanner {
	return w.scanner
}

// GetProcessor returns data processor
func (w *WizardImpl) GetProcessor() DataProcessor {
	return w.processor
}

// GetGenerator returns configuration generator
func (w *WizardImpl) GetGenerator() Generator {
	return w.generator
}

// GetPresenter returns presenter
func (w *WizardImpl) GetPresenter() Presenter {
	return w.presenter
}

// RunWithCustomConfig starts wizard with custom configuration
func (w *WizardImpl) RunWithCustomConfig(config InitData) error {
	w.data = &config
	return w.Run()
}

// SetupLogging configures logging
func (w *WizardImpl) SetupLogging(logFile string) error {
	file, err := os.OpenFile(logFile, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0666)
	if err != nil {
		return fmt.Errorf("failed to open log file: %v", err)
	}
	
	log.SetOutput(file)
	log.SetFlags(log.LstdFlags)
	log.SetPrefix("DEBUG: ")
	
	return nil
}

// Shutdown корректно завершает работу мастера
func (w *WizardImpl) Shutdown() {
	log.Printf("Shutting down initialization wizard")
	if w.app != nil {
		w.app.Stop()
	}
}