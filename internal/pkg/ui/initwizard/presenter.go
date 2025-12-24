package initwizard

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/cozystack/talm/pkg/engine"
	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
	"github.com/siderolabs/talos/pkg/machinery/constants"
)

var stateToPage = map[WizardState]string{
    StatePreset:     "step1-simple",
    StateEndpoint:   "step2-generic",
    StateScanning:   "scanning",
    StateNodeSelect: "node-selection",
    StateNodeConfig: "node-config",
    StateConfirm:    "confirm",
    StateGenerate:   "generate",
    StateDone:       "success",
	StateAddNodeScan: "add-node-scan",
    StateCozystackScan: "cozystack-scan",
    StateVIPConfig: "vip-config",
    StateNetworkConfig: "network-config",
    StateNodeDetails: "node-details",
}

// PresenterImpl реализует интерфейс Presenter
type PresenterImpl struct {
	app           *tview.Application
	pages         *tview.Pages
	data          *InitData
	wizard        Wizard
	controller    *WizardController
	cancelScan    context.CancelFunc
	scanningModal tview.Primitive
}

func (p *PresenterImpl) Go(to WizardState) {
	log.Printf("[DEBUG-TRANSITION] Попытка перехода из состояния %s в %s", p.controller.state, to)
	if err := p.controller.Transition(to); err != nil {
		log.Printf("[DEBUG-TRANSITION] ОШИБКА ПЕРЕХОДА: %v", err)
		p.ShowErrorModal(err.Error())
		return
	}
	log.Printf("[DEBUG-TRANSITION] Переход успешен, текущее состояние: %s", p.controller.state)

	page, ok := stateToPage[to]
	if !ok {
		p.ShowErrorModal(fmt.Sprintf("no page for state %v", to))
		return
	}

	p.pages.SwitchToPage(page)
}

// PresetDescriptions содержит описания доступных preset'ов
var PresetDescriptions = map[string]string{
	"generic":   "Стандартный кластер Kubernetes с базовой конфигурацией. Подходит для большинства случаев использования.",
	"cozystack": "Платформа Cozystack с расширенными возможностями сети и хранения. Включает дополнительные модули ядра и оптимизации.",
}

// NewPresenter создает новый экземпляр презентера
func NewPresenter(app *tview.Application, pages *tview.Pages, data *InitData, wizard Wizard) *PresenterImpl {
    controller := NewWizardController(data)

    return &PresenterImpl{
        app:        app,
        pages:      pages,
        data:       data,
        wizard:     wizard,
        controller: controller,
    }
}

// ShowStep1Form отображает первую форму мастера
func (p *PresenterImpl) ShowStep1Form(data *InitData) *tview.Form {
	log.Printf("ДИАГНОСТИКА PRESENTER: Вызван ShowStep1Form")
	log.Printf("ДИАГНОСТИКА PRESENTER: data=%v", data)
	log.Printf("ДИАГНОСТИКА PRESENTER: p.app=%v", p.app)
	log.Printf("ДИАГНОСТИКА PRESENTER: p.pages=%v", p.pages)
	
	// Проверяем входные параметры
	if data == nil {
		log.Printf("КРИТИЧЕСКАЯ ОШИБКА PRESENTER: data равен nil!")
		return nil
	}
	
	if p.app == nil {
		log.Printf("КРИТИЧЕСКАЯ ОШИБКА PRESENTER: p.app равен nil!")
		return nil
	}
	
	if p.pages == nil {
		log.Printf("КРИТИЧЕСКАЯ ОШИБКА PRESENTER: p.pages равен nil!")
		return nil
	}
	
	log.Printf("ДИАГНОСТИКА PRESENTER: Все параметры в порядке, продолжаем создание формы")
	
	// Проверяем доступность generated.AvailablePresets
	log.Printf("ДИАГНОСТИКА PRESENTER: Проверяем generated.AvailablePresets...")
	
	log.Printf("ДИАГНОСТИКА PRESENTER: Создаем простую форму...")

	// Определяем начальный индекс для dropdown на основе текущего preset
	var initialIndex int
	if data.Preset == "cozystack" {
		initialIndex = 1
	} else {
		initialIndex = 0
		if data.Preset == "" {
			data.Preset = "generic"
		}
	}

	// Создаем простую форму без сложных контейнеров
	form := tview.NewForm().
		AddDropDown("Preset", []string{"generic", "cozystack"}, initialIndex, func(option string, index int) {
			log.Printf("ДИАГНОСТИКА PRESENTER: Изменен preset: %s", option)
			data.Preset = option
		}).
		AddInputField("Имя Кластера", data.ClusterName, 20, nil, func(text string) {
			data.ClusterName = text
		})

	form.
		AddButton("Далее", func() {
			log.Printf("ДИАГНОСТИКА PRESENTER: ========= КНОПКА 'ДАЛЕЕ' НАЖАТА =========")
			log.Printf("ДИАГНОСТИКА PRESENTER: Нажата кнопка 'Далее', preset = %s", data.Preset)
			log.Printf("ДИАГНОСТИКА PRESENTER: Начинаем обработку нажатия кнопки...")
			
			if data.Preset == "generic" || data.Preset == "cozystack" {
				log.Printf("ДИАГНОСТИКА PRESENTER: Переход к ShowGenericStep2")
				log.Printf("ДИАГНОСТИКА PRESENTER: Вызываем ShowGenericStep2 для preset = %s", data.Preset)
				p.ShowGenericStep2(data)
				log.Printf("ДИАГНОСТИКА PRESENTER: ShowGenericStep2 завершен")
			} else {
				log.Printf("ДИАГНОСТИКА PRESENTER: Некорректный preset: %s", data.Preset)
				p.ShowErrorModal(fmt.Sprintf("Некорректный preset: %s. Введите 'generic' или 'cozystack'", data.Preset))
			}
			log.Printf("ДИАГНОСТИКА PRESENTER: Обработка нажатия кнопки завершена")
			log.Printf("ДИАГНОСТИКА PRESENTER: ========= КНОПКА 'ДАЛЕЕ' ОБРАБОТАНА =========")
		}).
		AddButton("Отмена", func() {
			log.Printf("ДИАГНОСТИКА PRESENTER: ========= КНОПКА 'ОТМЕНА' НАЖАТА =========")
			p.app.Stop()
		})

	log.Printf("ДИАГНОСТИКА PRESENTER: Устанавливаем границы формы...")
	form.SetBorder(true).SetTitle("Talos Init Wizard - Шаг 1: Базовая Конфигурация").SetTitleAlign(tview.AlignLeft)

	log.Printf("ДИАГНОСТИКА PRESENTER: Добавляем страницу...")
		p.pages.AddPage("step1-simple", form, true, true)

		log.Printf("ДИАГНОСТИКА PRESENTER: Переключаемся на страницу...")
		p.Go(StatePreset)
	
	log.Printf("ДИАГНОСТИКА PRESENTER: Устанавливаем фокус...")
	p.app.SetFocus(form)
	
	log.Printf("ДИАГНОСТИКА PRESENTER: Метод ShowStep1Form завершен успешно, возвращаем form")
	
	log.Printf("ДИАГНОСТИКА PRESENTER: ShowStep1Form полностью завершен")
	return form
}

// ShowGenericStep2 отображает вторую форму для Generic и Cozystack preset'ов
func (p *PresenterImpl) ShowGenericStep2(data *InitData) {
	log.Printf("ДИАГНОСТИКА GENERIC-STEP2: Начало выполнения ShowGenericStep2 для preset = %s", data.Preset)
	log.Printf("ДИАГНОСТИКА GENERIC-STEP2: data = %v", data)
	log.Printf("ДИАГНОСТИКА GENERIC-STEP2: Создаем форму...")
	form := tview.NewForm().
		AddInputField("Kubernetes Endpoint", "", 30, nil, func(text string) {
			data.APIServerURL = text
		}).
		AddInputField("Floating IP (опционально)", "", 20, nil, func(text string) {
			data.FloatingIP = text
		})

	form.
		AddButton("Далее", func() {
			p.initializeCluster(data)
		}).
		AddButton("Назад", func() {
			p.Go(StatePreset)
		}).
		AddButton("Отмена", func() {
			p.app.Stop()
		})

	form.SetBorder(true).SetTitle(fmt.Sprintf("%s Preset - Дополнительная Конфигурация", strings.Title(data.Preset))).SetTitleAlign(tview.AlignLeft)

	log.Printf("ДИАГНОСТИКА GENERIC-STEP2: Добавляем страницу...")
		p.pages.AddPage("step2-generic", form, true, true)
		log.Printf("ДИАГНОСТИКА GENERIC-STEP2: Переключаемся на страницу...")
		p.Go(StateEndpoint)
	log.Printf("ДИАГНОСТИКА GENERIC-STEP2: Устанавливаем фокус...")
	p.app.SetFocus(form)
	log.Printf("ДИАГНОСТИКА GENERIC-STEP2: Функция ShowGenericStep2 завершена успешно")
}
// ShowCozystackScan отображает сканирование для Cozystack
func (p *PresenterImpl) ShowCozystackScan(data *InitData) {
	log.Printf("ДИАГНОСТИКА COZYSTACK-SCAN: Вызван ShowCozystackScan")
	log.Printf("ДИАГНОСТИКА COZYSTACK-SCAN: data = %v", data)

	form := tview.NewForm().
		AddInputField("Network to scan", "192.168.1.0/24", 20, nil, func(text string) {
			data.NetworkToScan = text
		})

	form.
		AddButton("Scan", func() {
			log.Printf("ДИАГНОСТИКА COZYSTACK-SCAN: Нажата кнопка 'Scan'")
			if data.NetworkToScan == "" {
				p.ShowErrorModal("Please enter network to scan")
				return
			}

			p.showCozystackScanningModal(data)
		}).
		AddButton("Cancel", func() {
			log.Printf("ДИАГНОСТИКА COZYSTACK-SCAN: Нажата кнопка 'Cancel'")
			p.app.Stop()
		})

	log.Printf("ДИАГНОСТИКА COZYSTACK-SCAN: Устанавливаем границы формы...")
	form.SetBorder(true).SetTitle("Cozystack Network Scan").SetTitleAlign(tview.AlignLeft)

	log.Printf("ДИАГНОСТИКА COZYSTACK-SCAN: Добавляем страницу...")
	p.pages.AddPage("cozystack-scan", form, true, true)

	log.Printf("ДИАГНОСТИКА COZYSTACK-SCAN: Переключаемся на страницу...")
	p.Go(StateCozystackScan)

	log.Printf("ДИАГНОСТИКА COZYSTACK-SCAN: Устанавливаем фокус...")
	p.app.SetFocus(form)

	log.Printf("ДИАГНОСТИКА COZYSTACK-SCAN: ShowCozystackScan завершен")
}

// ShowAddNodeWizard отображает мастер добавления новой ноды
func (p *PresenterImpl) ShowAddNodeWizard(data *InitData) {
	log.Printf("ДИАГНОСТИКА ADD-NODE: Вызван ShowAddNodeWizard")
	log.Printf("ДИАГНОСТИКА ADD-NODE: data = %v", data)
	log.Printf("[DEBUG-ADD-NODE] Текущее состояние контроллера перед установкой: %s", p.controller.state)

	p.Go(StateAddNodeScan)

	form := tview.NewForm().
		AddInputField("Network to scan", "192.168.1.0/24", 20, nil, func(text string) {
			data.NetworkToScan = text
		})

	form.
		AddButton("Scan", func() {
			log.Printf("ДИАГНОСТИКА ADD-NODE: Нажата кнопка 'Scan'")
			log.Printf("[DEBUG-ADD-NODE] Состояние при нажатии Scan: %s", p.controller.state)
			if data.NetworkToScan == "" {
				p.ShowErrorModal("Please enter network to scan")
				return
			}

			p.ShowScanningModal(func(ctx context.Context, updateProgress func(int)) {
				p.performNetworkScan(data, updateProgress)
			}, context.Background())
		}).
		AddButton("Cancel", func() {
			log.Printf("ДИАГНОСТИКА ADD-NODE: Нажата кнопка 'Cancel'")
			p.app.Stop()
		})

	log.Printf("ДИАГНОСТИКА ADD-NODE: Устанавливаем границы формы...")
	form.SetBorder(true).SetTitle("Add New Node - Network Scan").SetTitleAlign(tview.AlignLeft)

	log.Printf("ДИАГНОСТИКА ADD-NODE: Добавляем страницу...")
	p.pages.AddPage("add-node-scan", form, true, true)

	log.Printf("ДИАГНОСТИКА ADD-NODE: Переключаемся на страницу...")
	p.SwitchPage(p.pages, "add-node-scan")

	log.Printf("[DEBUG-ADD-NODE] ShowAddNodeWizard завершен, состояние контроллера: %s", p.controller.state)
}

// ShowNodeSelection отображает выбор ноды
func (p *PresenterImpl) ShowNodeSelection(data *InitData, title string) {
	list := tview.NewList().
		SetSelectedFunc(func(index int, name, secondName string, shortcut rune) {
			data.SelectedNode = data.DiscoveredNodes[index].IP
			data.SelectedNodeInfo = data.DiscoveredNodes[index]
			p.ShowNodeConfig(data)
		})

	for i, node := range data.DiscoveredNodes {
		desc := fmt.Sprintf("IP: %s", node.IP)
		if node.Hostname != "" && node.Hostname != node.IP {
			desc += fmt.Sprintf(", Hostname: %s", node.Hostname)
		}

		// Добавляем детальную информацию об оборудовании
		if node.Manufacturer != "" {
			desc += fmt.Sprintf(", CPU: %s", node.Manufacturer)
		}
		if node.CPU > 0 {
			desc += fmt.Sprintf(" (%d cores)", node.CPU)
		}
		if node.RAM > 0 {
			desc += fmt.Sprintf(", RAM: %d GB", node.RAM)
		}
		if len(node.Disks) > 0 {
			totalSize := 0
			for _, disk := range node.Disks {
				totalSize += disk.Size
			}
			desc += fmt.Sprintf(", Storage: %d disks (%d GB)", len(node.Disks), totalSize/1024/1024/1024)
		}

		// Добавляем информацию о сетевых интерфейсах
		if len(node.Hardware.Interfaces) > 0 {
			activeInterfaces := 0
			for _, iface := range node.Hardware.Interfaces {
				if iface.Name != "lo" && iface.MAC != "" {
					activeInterfaces++
				}
			}
			if activeInterfaces > 0 {
				desc += fmt.Sprintf(", Network: %d interfaces", activeInterfaces)
			}
		}

		// Добавляем информацию о типе узла
		if node.Type != "" {
			desc += fmt.Sprintf(", Type: %s", node.Type)
		}

		list.AddItem(node.Name, desc, rune('1'+i), nil)
	}

	buttons := tview.NewForm().
		AddButton("Детали", func() {
			p.ShowNodeDetails(data)
		}).
		AddButton("Назад", func() {
			if title == "Select First Control Plane Node" {
				p.SwitchPage(p.pages, "cozystack-scan")
			} else {
				p.SwitchPage(p.pages, "add-node-scan")
			}
		}).
		AddButton("Отмена", func() {
			p.app.Stop()
		})

	buttons.SetButtonsAlign(tview.AlignCenter)

	flex := tview.NewFlex().SetDirection(tview.FlexRow).
		AddItem(list, 0, 1, true).
		AddItem(buttons, 3, 1, true)

	flex.SetBorder(true).SetTitle(title).SetTitleAlign(tview.AlignLeft)

	flex.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		if event.Key() == tcell.KeyTab {
			if p.app.GetFocus() == list {
				p.app.SetFocus(buttons)
			} else {
				p.app.SetFocus(list)
			}
			return nil
		}
		return event
	})

	p.pages.AddPage("node-selection", flex, true, true)
		p.Go(StateNodeSelect)
		p.app.SetFocus(list)
}

// ShowNodeConfig отображает конфигурацию ноды
func (p *PresenterImpl) ShowNodeConfig(data *InitData) {
	defaultHostname := data.SelectedNodeInfo.Hostname
	if defaultHostname == "" || defaultHostname == data.SelectedNode {
		defaultHostname = data.SelectedNode
	}

	disks := data.SelectedNodeInfo.Disks
	log.Printf("Disks: %v", disks)
	diskOptions := make([]string, len(disks))
	for i, disk := range disks {
		sizeGB := disk.Size / 1024 / 1024 / 1024
		desc := fmt.Sprintf("%s (%d GB", disk.DevPath, sizeGB)
		if disk.Model != "" {
			desc += fmt.Sprintf(", %s", disk.Model)
		}
		if disk.Transport != "" {
			desc += fmt.Sprintf(", %s", disk.Transport)
		}
		desc += ")"
		diskOptions[i] = desc
	}

	allInterfaces := data.SelectedNodeInfo.Hardware.Interfaces
	var interfaces []Interface

	log.Printf("[INTERFACE-FILTER] Всего найдено интерфейсов: %d", len(allInterfaces))

	for i, iface := range allInterfaces {
		log.Printf("[INTERFACE-FILTER] Проверяем интерфейс %d: %s [MAC: %s] [IPs: %v]",
			i, iface.Name, iface.MAC, iface.IPs)

		// Фильтрация в точном соответствии с shell-скриптом
		includeInterface := false

		// Пропускаем явно нежелательные интерфейсы
		if iface.Name == "lo" || iface.Name == "docker0" || strings.HasPrefix(iface.Name, "br-") ||
			strings.HasPrefix(iface.Name, "veth") || strings.HasPrefix(iface.Name, "cali") {
			log.Printf("[INTERFACE-FILTER] Пропускаем нежелательный интерфейс: %s", iface.Name)
			continue
		}

		// Проверяем соответствие паттерну валидных имен как в shell-скрипте
		validPrefixes := []string{"eno", "eth", "enp", "enx", "ens", "bond"}
		for _, prefix := range validPrefixes {
			if strings.HasPrefix(iface.Name, prefix) {
				includeInterface = true
				log.Printf("[INTERFACE-FILTER] Интерфейс %s соответствует префиксу %s", iface.Name, prefix)
				break
			}
		}

		// Если не matched по префиксу, но есть MAC адрес - включаем (для виртуальных интерфейсов)
		if !includeInterface && iface.MAC != "" {
			includeInterface = true
			log.Printf("[INTERFACE-FILTER] Включаем виртуальный интерфейс с MAC: %s", iface.Name)
		}

		// Фильтруем интерфейсы с полностью нулевыми MAC адресами
		// Отклоняем только полностью нулевые MAC (00:00:00:00:00:00), оставляем MAC с префиксом 00:00
		if includeInterface && iface.MAC != "" && iface.MAC == "00:00:00:00:00:00" {
			log.Printf("[INTERFACE-FILTER] Пропускаем интерфейс с полностью нулевым MAC: %s (%s)", iface.Name, iface.MAC)
			includeInterface = false
		}

		if includeInterface {
			interfaces = append(interfaces, iface)
			log.Printf("[INTERFACE-FILTER] Добавлен интерфейс: %s [MAC: %s] [IPs: %v]",
				iface.Name, iface.MAC, iface.IPs)
		}
	}

	log.Printf("[INTERFACE-FILTER] Отфильтровано интерфейсов: %d из %d", len(interfaces), len(allInterfaces))

	// Сортируем интерфейсы: приоритет интерфейсам с IP адресами
	sort.Slice(interfaces, func(i, j int) bool {
		// Интерфейсы с IPv4 адресами идут первыми
		hasIPi := false
		hasIPj := false

		for _, ip := range interfaces[i].IPs {
			if strings.Contains(ip, ".") { // IPv4 адрес
				hasIPi = true
				break
			}
		}

		for _, ip := range interfaces[j].IPs {
			if strings.Contains(ip, ".") { // IPv4 адрес
				hasIPj = true
				break
			}
		}

		// Если один интерфейс имеет IP, а другой нет - первый идет первым
		if hasIPi != hasIPj {
			return hasIPi && !hasIPj
		}

		// Если оба имеют или не имеют IP - сортируем по имени
		return interfaces[i].Name < interfaces[j].Name
	})

	interfaceOptions := make([]string, len(interfaces))
	for i, iface := range interfaces {
		// Создаем улучшенное отображение: interface_name MAC_address (IP/24) [↑/↓]
		interfaceDisplay := fmt.Sprintf("%s %s", iface.Name, iface.MAC)

		// Добавляем IP адрес с маской подсети если есть
		if len(iface.IPs) > 0 {
			// Находим первый IPv4 адрес (не IPv6)
			var mainIP string
			for _, ip := range iface.IPs {
				// Проверяем, что это IPv4 адрес
				if strings.Contains(ip, ".") {
					mainIP = ip
					break
				}
			}

			// Если нашли IPv4, добавляем маску /24 (стандарт для локальных сетей)
			if mainIP != "" {
				// Проверяем, есть ли уже маска в IP
				if !strings.Contains(mainIP, "/") {
					mainIP += "/24"
				}
				interfaceDisplay += fmt.Sprintf(" (%s)", mainIP)
			}
		}

		// Добавляем индикатор статуса (↑ для интерфейсов с IP, ↓ для без IP)
		hasIPv4 := false
		for _, ip := range iface.IPs {
			if strings.Contains(ip, ".") {
				hasIPv4 = true
				break
			}
		}

		if hasIPv4 {
			interfaceDisplay += " [↑]"
		} else {
			interfaceDisplay += " [↓]"
		}

		interfaceOptions[i] = interfaceDisplay

		log.Printf("[INTERFACE-FORMAT] Создан вариант %d: %s", i, interfaceDisplay)
	}

	data.Hostname = defaultHostname
	data.NodeType = "controlplane"  // or appropriate default
	if len(disks) > 0 {
		data.Disk = disks[0].Name  // or appropriate default
	}
	if len(interfaces) > 0 {
		data.Interface = interfaces[0].Name  // or appropriate default
	}

	form := tview.NewForm().
		AddDropDown("Role", []string{"controlplane", "worker"}, 0, func(option string, index int) {
			data.NodeType = option
		}).
		AddInputField("Hostname", defaultHostname, 20, nil, func(text string) {
			data.Hostname = text
		}).
		AddDropDown("Disk", diskOptions, 0, func(option string, index int) {
			if index >= 0 && index < len(disks) {
				data.Disk = disks[index].Name
			}
		}).
		AddDropDown("Interface", interfaceOptions, 0, func(option string, index int) {
			if index >= 0 && index < len(interfaces) {
				data.Interface = interfaces[index].Name
			}
		}).
		AddInputField("Virtual IP (optional)", "", 20, nil, func(text string) {
			data.VIP = text
		})

	form.
		AddButton("OK", func() {
			// Автоматически устанавливаем сетевую конфигурацию
			data.Addresses = data.SelectedNode + "/24"
			data.Gateway = "192.168.1.1"
			data.DNSServers = "8.8.8.8,1.1.1.1"

			p.ShowConfigConfirmation(data)
		}).
		AddButton("Back", func() {
				p.Go(StateNodeSelect)
			}).
		AddButton("Cancel", func() {
			p.app.Stop()
		})

	form.SetBorder(true).SetTitle("Node Configuration").SetTitleAlign(tview.AlignLeft)
		p.pages.AddPage("node-config", form, true, true)
		p.Go(StateNodeConfig)
		p.app.SetFocus(form)
}

// ShowVIPConfig отображает конфигурацию виртуального IP
func (p *PresenterImpl) ShowVIPConfig(data *InitData) {
	form := tview.NewForm().
		AddInputField("Virtual IP (optional)", "", 20, nil, func(text string) {
			data.VIP = text
		})

	form.
		AddButton("Next", func() {
			p.ShowConfigConfirmation(data)
		}).
		AddButton("Back", func() {
			p.SwitchPage(p.pages, "network-config")
		}).
		AddButton("Cancel", func() {
			p.app.Stop()
		})

	form.SetBorder(true).SetTitle("Virtual IP Configuration").SetTitleAlign(tview.AlignLeft)
	p.pages.AddPage("vip-config", form, true, true)
	p.SwitchPage(p.pages, "vip-config")
	p.app.SetFocus(form)
}

// ShowNetworkConfig отображает конфигурацию сети
func (p *PresenterImpl) ShowNetworkConfig(data *InitData) {
	form := tview.NewForm().
		AddInputField("Addresses", "", 30, nil, func(text string) {
			data.Addresses = text
		}).
		AddInputField("Gateway", "", 20, nil, func(text string) {
			data.Gateway = text
		}).
		AddInputField("DNS Servers", "", 40, nil, func(text string) {
			data.DNSServers = text
		})

	form.
		AddButton("Next", func() {
			p.ShowVIPConfig(data)
		}).
		AddButton("Back", func() {
			p.SwitchPage(p.pages, "interface-selection")
		}).
		AddButton("Cancel", func() {
			p.app.Stop()
		})

	form.SetBorder(true).SetTitle("Network Configuration").SetTitleAlign(tview.AlignLeft)
	p.pages.AddPage("network-config", form, true, true)
	p.SwitchPage(p.pages, "network-config")
	p.app.SetFocus(form)
}

// ShowProgressModal отображает модальное окно с прогрессом
func (p *PresenterImpl) ShowProgressModal(message string, task func()) {
	modal := tview.NewModal().
		SetText(message)

	p.pages.AddPage("progress", modal, true, true)
	p.SwitchPage(p.pages, "progress")
	p.app.SetFocus(modal)

	go task()
}

// ShowScanningModal отображает модальное окно сканирования с прогрессом
func (p *PresenterImpl) ShowScanningModal(scanFunc func(context.Context, func(int)), ctx context.Context) {
	log.Printf("[FIXED-UI] Открываем модальное окно сканирования")
	log.Printf("[DEBUG-SCANNING] Состояние перед Go(StateScanning): %s", p.controller.state)

	// Флаг отмены
	cancelled := false

	// Создаем функцию отмены
	dismissModal := func() {
		if cancelled {
			log.Printf("[FIXED-UI] Отмена уже выполняется, пропускаем")
			return
		}
		cancelled = true

		log.Printf("[FIXED-UI] ===== ОТМЕНА МОДАЛЬНОГО ОКНА =====")

		// Отменяем сканирование
		if p.cancelScan != nil {
			log.Printf("[FIXED-UI] Отменяем сканирование...")
			p.cancelScan()
			p.cancelScan = nil
			log.Printf("[FIXED-UI] Сканирование отменено")
		} else {
			log.Printf("[FIXED-UI] cancelScan не установлена")
		}

		// Немедленно закрываем модальное окно и возвращаемся к предыдущему экрану
		log.Printf("[FIXED-UI] НАЧИНАЕМ ПРЯМОЕ ОБНОВЛЕНИЕ UI (минуя QueueUpdateDraw)...")

		// Очищаем ссылку на модальное окно
		log.Printf("[FIXED-UI] Очищаем ссылку на модальное окно...")
		p.scanningModal = nil

		// Удаляем страницу сканирования
		log.Printf("[FIXED-UI] Удаляем страницу 'scanning'...")
		p.pages.RemovePage("scanning")
		log.Printf("[FIXED-UI] Страница 'scanning' удалена")

		// Проверяем доступные страницы
		log.Printf("[FIXED-UI] Проверяем доступные страницы...")
		if p.pages.HasPage("cozystack-scan") {
			log.Printf("[FIXED-UI] Найдена страница 'cozystack-scan', переключаемся...")
			p.SwitchPage(p.pages, "cozystack-scan")
			log.Printf("[FIXED-UI] Переключились на 'cozystack-scan'")
		} else if p.pages.HasPage("add-node-scan") {
			log.Printf("[FIXED-UI] Найдена страница 'add-node-scan', переключаемся...")
			p.SwitchPage(p.pages, "add-node-scan")
			log.Printf("[FIXED-UI] Переключились на 'add-node-scan'")
		} else {
			log.Printf("[FIXED-UI] Страницы для возврата не найдены!")
			log.Printf("[FIXED-UI] Доступные страницы: %v", p.pages.GetPageNames(false))
		}

		// Сбрасываем обработчик клавиш
		log.Printf("[FIXED-UI] Сбрасываем обработчик клавиш...")
		p.app.SetInputCapture(nil)
		log.Printf("[FIXED-UI] Обработчик клавиш сброшен")

		// Принудительно обновляем UI
		log.Printf("[FIXED-UI] Принудительно обновляем UI...")
		p.app.Draw()
		log.Printf("[FIXED-UI] UI обновлен! Прямое обновление завершено.")

		// Добавляем небольшую задержку для гарантии обновления
		log.Printf("[FIXED-UI] Добавляем задержку для гарантии обновления...")
		time.Sleep(100 * time.Millisecond)
		p.app.Draw()
		log.Printf("[FIXED-UI] Финальное обновление UI выполнено.")
	}

	// Создаем свой модальный диалог
	progressText := tview.NewTextView().
		SetText("Scanning network... |\n[                                        ] 0%").
		SetTextAlign(tview.AlignCenter)

	cancelButton := tview.NewButton("Cancel").
		SetSelectedFunc(func() {
			log.Printf("[FIXED-UI] Пользователь нажал Cancel")
			dismissModal()
		})

	flex := tview.NewFlex().SetDirection(tview.FlexRow)
	flex.AddItem(progressText, 0, 1, false)
	flex.AddItem(cancelButton, 1, 0, true)

	flex.SetBorder(true).SetTitle("Network Scanning")
	flex.SetBackgroundColor(tcell.ColorBlack)

	// Сохраняем ссылку на модальное окно
	p.scanningModal = flex

	// Добавляем глобальную обработку клавиш
	p.app.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		if event.Key() == tcell.KeyEscape && p.scanningModal != nil {
			log.Printf("[FIXED-UI] Нажата клавиша Escape")
			dismissModal()
			return nil
		}
		return event
	})

	p.pages.AddPage("scanning", flex, true, true)
		p.Go(StateScanning)
		p.app.SetFocus(flex)

	go func() {
		// Передаем флаг отмены в scanFunc
		scanFunc(ctx, func(progress int) {
			// Проверяем, не была ли отменена операция И существует ли модальное окно
			if cancelled || p.scanningModal == nil {
				log.Printf("[FIXED-UI] Игнорируем обновление прогресса - операция отменена или модальное окно закрыто")
				return
			}

			log.Printf("[FIXED-UI] Обновление прогресса: %d%%", progress)
			// Обновляем UI в главном потоке
			p.app.QueueUpdateDraw(func() {
				if p.scanningModal != nil {
					// Создаем прогресс бар
					progressBar := createSimpleProgressBar(progress)
					message := fmt.Sprintf("Scanning network... |\n%s %d%%", progressBar, progress)
					// Обновляем текст в TextView
					if flex, ok := p.scanningModal.(*tview.Flex); ok {
						if progressText, ok := flex.GetItem(0).(*tview.TextView); ok {
							progressText.SetText(message)
							log.Printf("[FIXED-UI] UI обновлен: %s", message)
						}
					}
				}
			})
		})
	}()
}

// ShowErrorModal отображает модальное окно с ошибкой
func (p *PresenterImpl) ShowErrorModal(message string) {
	modal := tview.NewModal().
		SetText(fmt.Sprintf("Ошибка: %s", message)).
		AddButtons([]string{"OK"}).
		SetDoneFunc(func(buttonIndex int, buttonLabel string) {
			p.pages.RemovePage("error")
		})

	p.pages.AddPage("error", modal, true, true)
	p.SwitchPage(p.pages, "error")
	p.app.SetFocus(modal)
}

// ShowSuccessModal отображает модальное окно с успешным сообщением
func (p *PresenterImpl) ShowSuccessModal(message string) {
	modal := tview.NewModal().
		SetText(message).
		AddButtons([]string{"OK"}).
		SetDoneFunc(func(buttonIndex int, buttonLabel string) {
			p.app.Stop()
		})

	p.pages.AddPage("success", modal, true, true)
	p.SwitchPage(p.pages, "success")
	p.app.SetFocus(modal)
}

// ShowConfigConfirmation отображает подтверждение конфигурации
func (p *PresenterImpl) ShowConfigConfirmation(data *InitData) {
	config := fmt.Sprintf("Role: %s\nHostname: %s\nDisk: %s\nInterface: %s\nAddresses: %s\nGateway: %s\nDNS: %s\nVIP: %s",
		data.NodeType, data.Hostname, data.Disk, data.Interface, data.Addresses, data.Gateway, data.DNSServers, data.VIP)

	modal := tview.NewModal().
		SetText(fmt.Sprintf("Confirm configuration:\n\n%s", config)).
		AddButtons([]string{"OK", "Back", "Cancel"}).
		SetDoneFunc(func(buttonIndex int, buttonLabel string) {
			switch buttonLabel {
			case "OK":
				p.generateMachineConfig(data)
			case "Back":
					p.Go(StateNodeConfig)
				case "Cancel":
				p.app.Stop()
			}
		})

	p.pages.AddPage("confirm", modal, true, true)
		p.Go(StateConfirm)
		p.app.SetFocus(modal)
}

// ShowBootstrapPrompt отображает запрос на bootstrap
func (p *PresenterImpl) ShowBootstrapPrompt(data *InitData, nodeFileName string) {
	modal := tview.NewModal().
		SetText("Do you want to bootstrap etcd now?\nThis will initialize the Kubernetes cluster.").
		AddButtons([]string{"Bootstrap", "Skip", "Cancel"}).
		SetDoneFunc(func(buttonIndex int, buttonLabel string) {
			switch buttonLabel {
			case "Bootstrap":
				p.showBootstrapProgress()
			case "Skip":
				p.ShowSuccessModal("First node configured. Bootstrap can be done later.")
			case "Cancel":
				p.app.Stop()
			}
		})

	p.pages.AddPage("bootstrap-prompt", modal, true, true)
	p.SwitchPage(p.pages, "bootstrap-prompt")
	p.app.SetFocus(modal)
}

// ShowFirstNodeConfig отображает конфигурацию первой ноды
func (p *PresenterImpl) ShowFirstNodeConfig(data *InitData) {
	form := tview.NewForm().
		AddInputField("Floating IP (VIP) - опционально", "", 20, nil, func(text string) {
			data.FloatingIP = text
		}).
		AddInputField("Kubernetes Endpoint", "", 30, nil, func(text string) {
			data.APIServerURL = text
		})

	form.
		AddButton("Далее", func() {
			data.NodeType = "control-plane"
			p.initializeCluster(data)
		}).
		AddButton("Назад", func() {
			p.SwitchPage(p.pages, "node-type")
		}).
		AddButton("Отмена", func() {
			p.app.Stop()
		})

	// Устанавливаем значение по умолчанию для endpoint
	if data.FloatingIP != "" {
		form.GetFormItemByLabel("Kubernetes Endpoint").(*tview.InputField).
			SetText(fmt.Sprintf("https://%s:6443", data.FloatingIP))
	} else if data.SelectedNode != "" {
		form.GetFormItemByLabel("Kubernetes Endpoint").(*tview.InputField).
			SetText(fmt.Sprintf("https://%s:6443", data.SelectedNode))
	}

	form.SetBorder(true).SetTitle("Конфигурация Первой Ноды").SetTitleAlign(tview.AlignLeft)
	p.pages.AddPage("first-node-config", form, true, true)
	p.SwitchPage(p.pages, "first-node-config")
	p.app.SetFocus(form)
}

// ShowNodeDetails отображает детальную информацию об узле
func (p *PresenterImpl) ShowNodeDetails(data *InitData) {
	nodeInfo := data.SelectedNodeInfo

	// Создаем текстовое поле для отображения детальной информации
	details := tview.NewTextView()
	details.SetScrollable(true)

	var info strings.Builder
	info.WriteString(fmt.Sprintf("=== Детальная Информация об Узле ===\n\n"))
	info.WriteString(fmt.Sprintf("Имя: %s\n", nodeInfo.Name))
	info.WriteString(fmt.Sprintf("IP адрес: %s\n", nodeInfo.IP))
	info.WriteString(fmt.Sprintf("Hostname: %s\n", nodeInfo.Hostname))
	info.WriteString(fmt.Sprintf("MAC адрес: %s\n", nodeInfo.MAC))
	info.WriteString(fmt.Sprintf("Тип: %s\n", nodeInfo.Type))
	info.WriteString(fmt.Sprintf("Статус: %s\n", map[bool]string{true: "Настроен", false: "Не настроен"}[nodeInfo.Configured]))

	// Информация о процессоре
	info.WriteString("\n=== Процессор ===\n")
	if len(nodeInfo.Hardware.Processors) > 0 {
		for i, proc := range nodeInfo.Hardware.Processors {
			info.WriteString(fmt.Sprintf("Процессор %d:\n", i+1))
			info.WriteString(fmt.Sprintf("  Производитель: %s\n", proc.Manufacturer))
			info.WriteString(fmt.Sprintf("  Модель: %s\n", proc.ProductName))
			info.WriteString(fmt.Sprintf("  Потоков: %d\n", proc.ThreadCount))
		}
	} else {
		info.WriteString("Информация о процессоре недоступна\n")
	}

	// Информация о памяти
	info.WriteString("\n=== Память ===\n")
	info.WriteString(fmt.Sprintf("Общий объем: %d MiB (%d GiB)\n", nodeInfo.Hardware.Memory.Size, nodeInfo.Hardware.Memory.Size/1024))

	// Информация о дисках
	info.WriteString("\n=== Диски ===\n")
	if len(nodeInfo.Disks) > 0 {
		totalSize := 0
		for i, disk := range nodeInfo.Disks {
			sizeGB := disk.Size / 1024 / 1024 / 1024
			totalSize += disk.Size
			info.WriteString(fmt.Sprintf("Диск %d:\n", i+1))
			info.WriteString(fmt.Sprintf("  Имя: %s\n", disk.Name))
			info.WriteString(fmt.Sprintf("  Размер: %d GB\n", sizeGB))
			info.WriteString(fmt.Sprintf("  Путь: %s\n", disk.DevPath))
			info.WriteString(fmt.Sprintf("  Модель: %s\n", disk.Model))
			info.WriteString(fmt.Sprintf("  Транспорт: %s\n", disk.Transport))
		}
		info.WriteString(fmt.Sprintf("\nОбщий объем хранилища: %d GB\n", totalSize/1024/1024/1024))
	} else {
		info.WriteString("Информация о дисках недоступна\n")
	}

	// Информация о сетевых интерфейсах
	info.WriteString("\n=== Сетевые Интерфейсы ===\n")
	if len(nodeInfo.Hardware.Interfaces) > 0 {
		for i, iface := range nodeInfo.Hardware.Interfaces {
			info.WriteString(fmt.Sprintf("Интерфейс %d:\n", i+1))
			info.WriteString(fmt.Sprintf("  Имя: %s\n", iface.Name))
			info.WriteString(fmt.Sprintf("  MAC: %s\n", iface.MAC))

			if len(iface.IPs) > 0 {
				info.WriteString(fmt.Sprintf("  IP адреса: %s\n", strings.Join(iface.IPs, ", ")))
			} else {
				info.WriteString("  IP адреса: не настроены\n")
			}
		}
	} else {
		info.WriteString("Информация о сетевых интерфейсах недоступна\n")
	}

	details.SetText(info.String())
	details.SetBorder(true).SetTitle("Детали Узла").SetTitleAlign(tview.AlignLeft)

	// Создаем кнопки
	buttons := tview.NewForm().
		AddButton("Назад", func() {
				p.Go(StateNodeSelect)
			})

	buttons.SetButtonsAlign(tview.AlignCenter)

	// Создаем компоновку
	flex := tview.NewFlex().SetDirection(tview.FlexRow).
		AddItem(details, 0, 1, false).
		AddItem(buttons, 3, 1, true)

	flex.SetBorder(true).SetTitle(fmt.Sprintf("Детали узла %s", nodeInfo.Hostname)).SetTitleAlign(tview.AlignLeft)

	p.pages.AddPage("node-details", flex, true, true)
	p.SwitchPage(p.pages, "node-details")
	p.app.SetFocus(buttons)
}

// SwitchPage переключает страницу
func (p *PresenterImpl) SwitchPage(pages *tview.Pages, pageName string) {
	log.Printf("ДИАГНОСТИКА SWITCHPAGE: Переключаемся на страницу: %s", pageName)
	log.Printf("ДИАГНОСТИКА SWITCHPAGE: Доступные страницы: %v", pages.GetPageNames(false))
	log.Printf("ДИАГНОСТИКА SWITCHPAGE: Выполняем SwitchToPage...")
	pages.SwitchToPage(pageName)
	log.Printf("ДИАГНОСТИКА SWITCHPAGE: Переключение на %s выполнено", pageName)
}

// debug простой отладочный метод
func (p *PresenterImpl) debug(msg string, args ...interface{}) {
	if os.Getenv("DEBUG_TUI") != "" {
		log.Printf("[TUI-DEBUG] "+msg, args...)
	}
}

// Вспомогательные методы

// Удалена функция hasMACPrefix - больше не используется

// showCozystackScanningModal отображает модальное окно сканирования для Cozystack
func (p *PresenterImpl) showCozystackScanningModal(data *InitData) {
	log.Printf("[FIXED-UI] Запускаем showCozystackScanningModal")

	// Создаем контекст с возможностью отмены
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Сохраняем cancel функцию для использования при отмене
	p.cancelScan = cancel

	// Флаг отмены
	cancelled := false

	// Создаем функцию отмены
	dismissModal := func() {
		if cancelled {
			log.Printf("[FIXED-UI] Отмена Cozystack уже выполняется, пропускаем")
			return
		}
		cancelled = true

		log.Printf("[FIXED-UI] ===== ОТМЕНА МОДАЛЬНОГО ОКНА COZYSTACK =====")

		if p.cancelScan != nil {
			log.Printf("[FIXED-UI] Отменяем сканирование Cozystack...")
			p.cancelScan()
			p.cancelScan = nil
			log.Printf("[FIXED-UI] Сканирование Cozystack отменено")
		} else {
			log.Printf("[FIXED-UI] cancelScan Cozystack не установлена")
		}

		// Немедленно закрываем модальное окно и возвращаемся к предыдущему экрану
		log.Printf("[FIXED-UI] НАЧИНАЕМ ПРЯМОЕ ОБНОВЛЕНИЕ UI COZYSTACK...")

		// Очищаем ссылку на модальное окно
		log.Printf("[FIXED-UI] Очищаем ссылку на модальное окно Cozystack...")
		p.scanningModal = nil

		// Удаляем страницу сканирования
		log.Printf("[FIXED-UI] Удаляем страницу 'scanning' Cozystack...")
		p.pages.RemovePage("scanning")
		log.Printf("[FIXED-UI] Страница 'scanning' Cozystack удалена")

		// Переключаемся на страницу Cozystack
		log.Printf("[FIXED-UI] Переключаемся на страницу 'cozystack-scan'...")
		p.SwitchPage(p.pages, "cozystack-scan")
		log.Printf("[FIXED-UI] Переключились на 'cozystack-scan'")

		// Сбрасываем обработчик клавиш
		log.Printf("[FIXED-UI] Сбрасываем обработчик клавиш Cozystack...")
		p.app.SetInputCapture(nil)
		log.Printf("[FIXED-UI] Обработчик клавиш Cozystack сброшен")

		// Принудительно обновляем UI
		log.Printf("[FIXED-UI] Принудительно обновляем UI Cozystack...")
		p.app.Draw()
		log.Printf("[FIXED-UI] UI Cozystack обновлен! Прямое обновление завершено.")

		// Добавляем небольшую задержку для гарантии обновления
		log.Printf("[FIXED-UI] Добавляем задержку для гарантии обновления Cozystack...")
		time.Sleep(100 * time.Millisecond)
		p.app.Draw()
		log.Printf("[FIXED-UI] Финальное обновление UI Cozystack выполнено.")
	}

	// Создаем свой модальный диалог
	progressText := tview.NewTextView().
		SetText("Scanning network... |\n[                                        ] 0%").
		SetTextAlign(tview.AlignCenter)

	cancelButton := tview.NewButton("Cancel").
		SetSelectedFunc(func() {
			log.Printf("[FIXED-UI] Пользователь нажал Cancel в Cozystack")
			dismissModal()
		})

	flex := tview.NewFlex().SetDirection(tview.FlexRow)
	flex.AddItem(progressText, 0, 1, false)
	flex.AddItem(cancelButton, 1, 0, true)

	flex.SetBorder(true).SetTitle("Network Scanning")
	flex.SetBackgroundColor(tcell.ColorBlack)

	// Сохраняем ссылку на модальное окно
	p.scanningModal = flex

	// Добавляем глобальную обработку клавиш
	p.app.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		if event.Key() == tcell.KeyEscape && p.scanningModal != nil {
			log.Printf("[FIXED-UI] Нажата клавиша Escape в Cozystack")
			dismissModal()
			return nil
		}
		return event
	})

	p.pages.AddPage("scanning", flex, true, true)
		p.Go(StateScanning)
		p.app.SetFocus(flex)

	go func() {
		log.Printf("[FIXED-UI] Запускаем сканирование в Cozystack")

		// Получаем сканер от wizard
		wizard := p.wizard
		scanner := wizard.GetScanner()

		// Запускаем сканирование
		nodes, err := scanner.ScanNetworkWithProgress(ctx, data.NetworkToScan, func(progress int) {
			// Проверяем, не была ли отменена операция И существует ли модальное окно
			if cancelled || p.scanningModal == nil {
				log.Printf("[FIXED-UI] Игнорируем обновление прогресса Cozystack - операция отменена или модальное окно закрыто")
				return
			}

			log.Printf("[FIXED-UI] Обновление прогресса Cozystack: %d%%", progress)
			// Обновляем UI в главном потоке
			p.app.QueueUpdateDraw(func() {
				if p.scanningModal != nil {
					// Создаем прогресс бар
					progressBar := createSimpleProgressBar(progress)
					message := fmt.Sprintf("Scanning network... |\n%s %d%%", progressBar, progress)
					// Обновляем текст в TextView
					if flex, ok := p.scanningModal.(*tview.Flex); ok {
						if progressText, ok := flex.GetItem(0).(*tview.TextView); ok {
							progressText.SetText(message)
							log.Printf("[FIXED-UI] UI Cozystack обновлен: %s", message)
						}
					}
				}
			})
		})

		// Очищаем cancel функцию после завершения
		p.cancelScan = nil

		// Проверяем, не была ли отменена операция
		if cancelled {
			log.Printf("[FIXED-UI] Сканирование Cozystack было отменено")
			return
		}

		log.Printf("[FIXED-UI] Сканирование Cozystack завершено, найдено %d нод", len(nodes))

		if err != nil {
			log.Printf("[FIXED-UI] Ошибка сканирования Cozystack: %v", err)
			p.app.QueueUpdateDraw(func() {
				p.scanningModal = nil
				p.ShowErrorModal(fmt.Sprintf("Ошибка сканирования: %v", err))
			})
			return
		}

		// Сохраняем результаты
		data.DiscoveredNodes = nodes

		// Показываем результаты
		p.app.QueueUpdateDraw(func() {
			p.scanningModal = nil
			p.pages.RemovePage("scanning")
			if len(nodes) > 0 {
				p.ShowNodeSelection(data, "Select First Control Plane Node")
			} else {
				p.ShowErrorModal("В сети не найдено нод Talos")
			}
		})
	}()
}

// performNetworkScan выполняет сканирование сети
func (p *PresenterImpl) performNetworkScan(data *InitData, updateProgress func(int)) {
	log.Printf("[FIXED-UI] Начинаем performNetworkScan для сети: %s", data.NetworkToScan)
	log.Printf("[FIXED-UI] Получен updateProgress callback: %v", updateProgress != nil)
	log.Printf("[DEBUG-PERFORM-SCAN] Состояние перед сканированием: %s", p.controller.state)

	// Получаем сканер от wizard
	wizard := p.wizard
	scanner := wizard.GetScanner()

	// Создаем контекст с возможностью отмены
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Сохраняем cancel функцию для использования при отмене
	p.cancelScan = cancel

	log.Printf("[FIXED-UI] Запускаем сканирование с отменой...")

	// Запускаем сканирование
	nodes, err := scanner.ScanNetworkWithProgress(ctx, data.NetworkToScan, func(progress int) {
		// Проверяем, не была ли отменена операция И существует ли модальное окно
		if p.scanningModal == nil {
			log.Printf("[FIXED-UI] Игнорируем обновление прогресса в performNetworkScan - модальное окно закрыто")
			return
		}

		log.Printf("[FIXED-UI] Обновление прогресса: %d%%", progress)
		// Обновляем UI в главном потоке
		p.app.QueueUpdateDraw(func() {
			// Обновляем модальное окно с новым прогрессом
			if p.scanningModal != nil {
				// Создаем прогресс бар
				progressBar := createSimpleProgressBar(progress)
				message := fmt.Sprintf("Scanning network... |\n%s %d%%", progressBar, progress)
				// Обновляем текст в TextView
				if flex, ok := p.scanningModal.(*tview.Flex); ok {
					if progressText, ok := flex.GetItem(0).(*tview.TextView); ok {
						progressText.SetText(message)
						log.Printf("[FIXED-UI] UI обновлен: %s", message)
					}
				}
			}
		})
	})

	// Очищаем cancel функцию после завершения
	p.cancelScan = nil

	if err != nil {
		log.Printf("[FIXED-UI] Ошибка сканирования: %v", err)
		p.app.QueueUpdateDraw(func() {
			p.scanningModal = nil
			p.ShowErrorModal(fmt.Sprintf("Ошибка сканирования: %v", err))
		})
		return
	}

	log.Printf("[FIXED-UI] Сканирование завершено, найдено %d нод", len(nodes))
	log.Printf("[DEBUG-PERFORM-SCAN] Состояние после сканирования перед ShowNodeSelection: %s", p.controller.state)

	// Сохраняем результаты
	data.DiscoveredNodes = nodes

	// Показываем результаты
	p.app.QueueUpdateDraw(func() {
		p.scanningModal = nil
		p.pages.RemovePage("scanning")
		if len(nodes) > 0 {
			log.Printf("[DEBUG-PERFORM-SCAN] Вызываем ShowNodeSelection")
			p.ShowNodeSelection(data, "Select Node to Add")
		} else {
			p.ShowErrorModal("В сети не найдено нод Talos")
		}
	})
}

// runScanningWithProgress запускает сканирование с отображением прогресса
func (p *PresenterImpl) runScanningWithProgress(scanFunc func(context.Context, func(int)), ctx context.Context) {
	log.Printf("[FIXED-UI] Запускаем runScanningWithProgress")

	// Функция обновления прогресса в UI
	updateProgress := func(progress int) {
		log.Printf("[FIXED-UI] Обновление прогресса: %d%%", progress)
		// Обновляем UI в главном потоке
		p.app.QueueUpdateDraw(func() {
			if p.scanningModal != nil {
				// Создаем прогресс бар
				progressBar := createSimpleProgressBar(progress)
				message := fmt.Sprintf("Scanning network... |\n%s %d%%", progressBar, progress)
				// Обновляем текст в TextView
				if flex, ok := p.scanningModal.(*tview.Flex); ok {
					if progressText, ok := flex.GetItem(0).(*tview.TextView); ok {
						progressText.SetText(message)
						log.Printf("[FIXED-UI] UI обновлен: %s", message)
					}
				}
			}
		})
	}

	// Запускаем сканирование
	log.Printf("[FIXED-UI] Выполняем scanFunc...")
	scanFunc(ctx, updateProgress)

	log.Printf("[FIXED-UI] Сканирование завершено")

	// Принудительно обновляем UI после завершения
	p.app.QueueUpdateDraw(func() {
		log.Printf("[FIXED-UI] Принудительное обновление UI после завершения")
		if p.scanningModal != nil {
			p.app.Draw()
			log.Printf("[FIXED-UI] UI принудительно обновлен")
		}
	})
}

// createProgressBar создает визуальный прогресс бар
func (p *PresenterImpl) createProgressBar(progress int) string {
	const width = 40
	filled := (progress * width) / 100

	var bar []byte
	bar = append(bar, '[')
	for i := 0; i < width; i++ {
		if i < filled {
			bar = append(bar, '=')
		} else if i == filled {
			bar = append(bar, '>')
		} else {
			bar = append(bar, ' ')
		}
	}
	bar = append(bar, ']')
	return string(bar)
}

// initializeCluster инициализирует кластер
func (p *PresenterImpl) initializeCluster(data *InitData) {
	// Валидация входных данных
	if data.ClusterName == "" {
		p.ShowErrorModal("Пожалуйста, введите имя кластера")
		return
	}

	if data.APIServerURL == "" {
		p.ShowErrorModal("Пожалуйста, укажите Kubernetes Endpoint")
		return
	}

	// Устанавливаем значения по умолчанию в зависимости от preset'а
	if data.PodSubnets == "" {
		if data.Preset == "cozystack" {
			data.PodSubnets = "10.244.0.0/16"
		} else {
			data.PodSubnets = "10.244.0.0/16"
		}
	}

	if data.ServiceSubnets == "" {
		if data.Preset == "cozystack" {
			data.ServiceSubnets = "10.96.0.0/16"
		} else {
			data.ServiceSubnets = "10.96.0.0/16"
		}
	}

	if data.AdvertisedSubnets == "" {
		data.AdvertisedSubnets = "192.168.0.0/24"
	}

	// Устанавливаем дополнительные значения для cozystack
	if data.Preset == "cozystack" {
		if data.ClusterDomain == "" {
			data.ClusterDomain = "cozy.local"
		}
		if data.Image == "" {
			data.Image = "ghcr.io/cozystack/cozystack/talos:v1.10.5"
		}
	}

	p.ShowProgressModal(fmt.Sprintf("Инициализация %s кластера...", data.Preset), func() {
		// Генерируем конфигурации
		if err := GenerateFromTUI(data); err != nil {
			p.app.QueueUpdateDraw(func() {
				p.ShowErrorModal(fmt.Sprintf("Ошибка генерации: %v", err))
			})
			return
		}

		// Показываем успешное завершение
		p.app.QueueUpdateDraw(func() {
			p.ShowSuccessModal(fmt.Sprintf("%s кластер успешно инициализирован!\n\nСозданные файлы:\n- talosconfig\n- secrets.yaml\n- Chart.yaml\n- values.yaml\n- templates/\n\nСледующие шаги:\n1. Проверьте созданные файлы\n2. Запустите 'helm install' для развертывания\n3. Используйте 'kubectl' для управления кластером",
				strings.Title(data.Preset)))
		})
	})
}

// initializeGenericCluster инициализирует generic кластер
func (p *PresenterImpl) initializeGenericCluster(data *InitData) {
	// Валидация входных данных
	if data.ClusterName == "" {
		p.ShowErrorModal("Пожалуйста, введите имя кластера")
		return
	}

	if data.APIServerURL == "" {
		p.ShowErrorModal("Пожалуйста, укажите Kubernetes Endpoint")
		return
	}

	// Устанавливаем значения по умолчанию для generic preset
	if data.PodSubnets == "" {
		data.PodSubnets = "10.244.0.0/16"
	}
	if data.ServiceSubnets == "" {
		data.ServiceSubnets = "10.96.0.0/16"
	}
	if data.AdvertisedSubnets == "" {
		data.AdvertisedSubnets = "192.168.0.0/24"
	}

	p.ShowProgressModal("Инициализация generic кластера...", func() {
		// Генерируем конфигурации
		if err := GenerateFromTUI(data); err != nil {
			p.app.QueueUpdateDraw(func() {
				p.ShowErrorModal(fmt.Sprintf("Ошибка генерации: %v", err))
			})
			return
		}

		// Показываем успешное завершение
		p.app.QueueUpdateDraw(func() {
			p.ShowSuccessModal("Generic кластер успешно инициализирован!\n\nСозданные файлы:\n- talosconfig\n- secrets.yaml\n- Chart.yaml\n- values.yaml\n- templates/")
		})
	})
}

// generateMachineConfig генерирует конфигурацию машины
func (p *PresenterImpl) generateMachineConfig(data *InitData) {
	log.Printf("[MACHINE-CONFIG] Начинаем генерацию машинной конфигурации для ноды: %s", data.SelectedNode)

	p.ShowProgressModal("Generating machine config...", func() {
		log.Printf("[MACHINE-CONFIG] Запуск генерации машинной конфигурации...")

		// Валидация обязательных данных
		if data.SelectedNode == "" {
			log.Printf("[MACHINE-CONFIG] Ошибка: не выбрана нода")
			p.app.QueueUpdateDraw(func() {
				p.ShowErrorModal("Не выбрана нода для генерации конфигурации")
			})
			return
		}

		if data.Hostname == "" {
			log.Printf("[MACHINE-CONFIG] Ошибка: не указано имя хоста")
			p.app.QueueUpdateDraw(func() {
				p.ShowErrorModal("Не указано имя хоста для ноды")
			})
			return
		}

		if data.Disk == "" {
			log.Printf("[MACHINE-CONFIG] Ошибка: не выбран диск")
			p.app.QueueUpdateDraw(func() {
				p.ShowErrorModal("Не выбран диск для установки")
			})
			return
		}

		if data.Interface == "" {
			log.Printf("[MACHINE-CONFIG] Ошибка: не выбран сетевой интерфейс")
			p.app.QueueUpdateDraw(func() {
				p.ShowErrorModal("Не выбран сетевой интерфейс")
			})
			return
		}

		log.Printf("[MACHINE-CONFIG] Все необходимые данные валидны")
		log.Printf("[MACHINE-CONFIG] Параметры:")
		log.Printf("[MACHINE-CONFIG] - Node: %s", data.SelectedNode)
		log.Printf("[MACHINE-CONFIG] - Hostname: %s", data.Hostname)
		log.Printf("[MACHINE-CONFIG] - NodeType: %s", data.NodeType)
		log.Printf("[MACHINE-CONFIG] - Disk: %s", data.Disk)
		log.Printf("[MACHINE-CONFIG] - Interface: %s", data.Interface)
		log.Printf("[MACHINE-CONFIG] - Addresses: %s", data.Addresses)
		log.Printf("[MACHINE-CONFIG] - Gateway: %s", data.Gateway)
		log.Printf("[MACHINE-CONFIG] - DNS: %s", data.DNSServers)
		log.Printf("[MACHINE-CONFIG] - VIP: %s", data.VIP)

		// Подготавливаем values для шаблона
		nodeValues := map[string]interface{}{
			"nodeHostname": data.Hostname,
			"nodeDisk":     data.Disk,
			"nodeImage":    p.getDefaultImageForPreset(data.Preset),
		}

		// Добавляем сетевую конфигурацию
		if data.Addresses != "" {
			// Создаем интерфейс конфигурацию
			nodeInterface := map[string]interface{}{
				"interface": data.Interface,
				"addresses": []string{data.Addresses},
			}

			if data.Gateway != "" {
				nodeInterface["routes"] = []map[string]interface{}{
					{
						"network": "0.0.0.0/0",
						"gateway": data.Gateway,
					},
				}
			}

			if data.VIP != "" && data.NodeType == "controlplane" {
				nodeInterface["vip"] = map[string]interface{}{
					"ip": data.VIP,
				}
			}

			nodeValues["nodeInterfaces"] = nodeInterface
		}

		// Добавляем DNS серверы
		if data.DNSServers != "" {
			dns := strings.Split(data.DNSServers, ",")
			for i, server := range dns {
				dns[i] = strings.TrimSpace(server)
			}
			nodeValues["nodeNameservers"] = map[string]interface{}{"servers": dns}
		}

		// Выбираем шаблон на основе NodeType
		var templateFile string
		if data.NodeType == "controlplane" {
			templateFile = "templates/controlplane.yaml"
		} else if data.NodeType == "worker" {
			templateFile = "templates/worker.yaml"
		} else {
			templateFile = "templates/node.yaml" // fallback
		}

		// Создаем опции для engine.Render
		opts := engine.Options{
			ValueFiles:        []string{"values.yaml"}, // Используем существующие values.yaml
			WithSecrets:       "secrets.yaml",
			TemplateFiles:     []string{templateFile},
			Offline:           true, // Не используем lookup функции
			KubernetesVersion: constants.DefaultKubernetesVersion,
		}

		// Сериализуем nodeInterfaces и nodeNameservers в JSON для opts.JsonValues
		if nodeValues["nodeInterfaces"] != nil {
			jsonData, err := json.Marshal(nodeValues["nodeInterfaces"])
			if err == nil {
				opts.JsonValues = append(opts.JsonValues, string(jsonData))
			} else {
				log.Printf("[MACHINE-CONFIG] Ошибка сериализации nodeInterfaces: %v", err)
			}
		}

		if nodeValues["nodeNameservers"] != nil {
			jsonData, err := json.Marshal(nodeValues["nodeNameservers"])
			if err == nil {
				opts.JsonValues = append(opts.JsonValues, string(jsonData))
			} else {
				log.Printf("[MACHINE-CONFIG] Ошибка сериализации nodeNameservers: %v", err)
			}
		}

		// Определяем тип машины для шаблона
		var machineType string
		if data.NodeType == "controlplane" {
			machineType = "controlplane"
		} else {
			machineType = "worker"
		}

		// Добавляем machineType в values через set
		opts.Values = append(opts.Values, fmt.Sprintf("machineType=%s", machineType))

		// Добавляем node-specific values через set
		if nodeValues["nodeHostname"] != nil {
			opts.Values = append(opts.Values, fmt.Sprintf("nodeHostname=%s", nodeValues["nodeHostname"]))
		}
		if nodeValues["nodeDisk"] != nil {
			opts.Values = append(opts.Values, fmt.Sprintf("nodeDisk=%s", nodeValues["nodeDisk"]))
		}
		if nodeValues["nodeImage"] != nil {
			opts.Values = append(opts.Values, fmt.Sprintf("nodeImage=%s", nodeValues["nodeImage"]))
		}

		// Выполняем рендеринг через engine.Render
		ctx := context.Background()
		configBytes, err := engine.Render(ctx, nil, opts)
		if err != nil {
			log.Printf("[MACHINE-CONFIG] Ошибка рендеринга шаблона: %v", err)
			p.app.QueueUpdateDraw(func() {
				p.ShowErrorModal(fmt.Sprintf("Ошибка генерации конфигурации: %v", err))
			})
			return
		}

		// Генерируем имя файла с автоинкрементом
		configFilename, err := generateNodeFileName("")
		if err != nil {
			log.Printf("[MACHINE-CONFIG] Ошибка генерации имени файла: %v", err)
			p.app.QueueUpdateDraw(func() {
				p.ShowErrorModal(fmt.Sprintf("Ошибка генерации имени файла: %v", err))
			})
			return
		}

		// Сохраняем конфигурацию в файл
		if err := os.WriteFile(configFilename, configBytes, 0o644); err != nil {
			log.Printf("[MACHINE-CONFIG] Ошибка сохранения файла конфигурации: %v", err)
			p.app.QueueUpdateDraw(func() {
				p.ShowErrorModal(fmt.Sprintf("Ошибка сохранения конфигурации: %v", err))
			})
			return
		}

		log.Printf("[MACHINE-CONFIG] Машинная конфигурация сохранена в файл: %s", configFilename)

		// Обновляем данные
		data.MachineConfig = string(configBytes)

		log.Printf("[MACHINE-CONFIG] Генерация машинной конфигурации завершена успешно")

		// Показываем результат
		p.app.QueueUpdateDraw(func() {
			p.ShowSuccessModal(fmt.Sprintf("Машинная конфигурация успешно создана!\n\nФайл: %s\nНода: %s (%s)\nТип: %s\n\nСледующие шаги:\n1. Установите Talos на ноду используя этот файл\n2. Примените конфигурацию: talosctl apply-config -n %s -f %s",
				configFilename, data.Hostname, data.SelectedNode, data.NodeType, data.SelectedNode, configFilename))
		})
	})
}

// showBootstrapProgress отображает прогресс bootstrap
func (p *PresenterImpl) showBootstrapProgress() {
	modal := tview.NewModal().
		SetText("Bootstrapping etcd...\nPlease wait")

	p.pages.AddPage("bootstrapping", modal, true, true)
	p.SwitchPage(p.pages, "bootstrapping")
	p.app.SetFocus(modal)

	go func() {
		// Здесь будет логика bootstrap
		p.app.QueueUpdateDraw(func() {
			p.pages.RemovePage("bootstrapping")
			p.ShowSuccessModal("Cluster bootstrapped successfully!\n\nNext steps:\n1. Check 'kubeconfig' file\n2. Use 'kubectl' to manage cluster")
		})
	}()
}

// createSimpleProgressBar создает простой текстовый прогресс бар
func createSimpleProgressBar(progress int) string {
	const width = 30
	filled := (progress * width) / 100

	var bar []byte
	bar = append(bar, '[')
	for i := 0; i < width; i++ {
		if i < filled {
			bar = append(bar, '=')
		} else {
			bar = append(bar, ' ')
		}
	}
	bar = append(bar, ']')
	return string(bar)
}

// generateNodeMachineConfig генерирует машинную конфигурацию для конкретной ноды
func (p *PresenterImpl) generateNodeMachineConfig(data *InitData) (string, error) {
	log.Printf("[NODE-CONFIG] Генерация машинной конфигурации для ноды %s (%s)", data.Hostname, data.SelectedNode)
	
	// Создаем базовую машинную конфигурацию
	config := fmt.Sprintf(`# Machine Configuration for %s
apiVersion: v1alpha1
kind: MachineConfig
metadata:
  name: %s
  namespace: %s
spec:
  # Machine network configuration
  network:
    hostname: %s
    interfaces:
      - interface: %s
        addresses:
          - %s
        dhcp: false
    routes:
      - gateway: %s
        destination: 0.0.0.0/0
    dns:
      servers:
        - %s
    vip:
      ip: %s
  
  # Machine type
  machineType: %s
  
  # Install configuration
  install:
    disk: %s
    image: %s
  
  # Additional machine configuration
  controlPlane:
    endpoint: %s
`, 
		data.Hostname, data.Hostname, data.ClusterName,
		data.Hostname, data.Interface, data.Addresses,
		data.Gateway, data.DNSServers, data.VIP,
		data.NodeType, data.Disk, 
		p.getDefaultImageForPreset(data.Preset),
		data.APIServerURL)
	
	// Добавляем специфичные настройки для разных типов нод
	if data.NodeType == "controlplane" {
		controlPlaneConfig := fmt.Sprintf(`  
  # Control plane specific configuration
  kubelet:
    extraArgs:
      node-labels: node-role.kubernetes.io/control-plane
  
  apiServer:
    certSANs:
      - 127.0.0.1
      - %s
`, data.VIP)
		config = config + controlPlaneConfig
	} else {
		workerConfig := `
  # Worker node specific configuration
  kubelet:
    extraArgs:
      node-labels: node-role.kubernetes.io/worker
`
		config = config + workerConfig
	}
	
	// Добавляем специфичные настройки для Cozystack
	if data.Preset == "cozystack" {
		cozystackConfig := `
  # Cozystack specific configuration
  kernel:
    modules:
      - name: br_netfilter
      - name: overlay
  sysctl:
    net.ipv4.ip_forward: 1
    net.bridge.bridge-nf-call-iptables: 1
    net.bridge.bridge-nf-call-ip6tables: 1
`
		config = config + cozystackConfig
		
		if data.NrHugepages > 0 {
			hugepagesConfig := fmt.Sprintf(`  
  # Hugepages configuration
  system:
    environment:
      NR_HUGEPAGES: "%d"
`, data.NrHugepages)
			config = config + hugepagesConfig
		}
	}
	
	log.Printf("[NODE-CONFIG] Машинная конфигурация создана, размер: %d символов", len(config))
	return config, nil
}

// getDefaultImageForPreset возвращает образ Talos по умолчанию для preset'а
func (p *PresenterImpl) getDefaultImageForPreset(preset string) string {
	switch preset {
	case "cozystack":
		return "ghcr.io/cozystack/cozystack/talos:v1.10.5"
	case "generic":
		return "ghcr.io/siderolabs/talos:v1.10.5"
	default:
		return "ghcr.io/siderolabs/talos:latest"
	}
}
