package interactive

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"

	"github.com/cozystack/talm/internal/pkg/ui/initwizard"
)

// Wizard основной интерфейс интерактивного мастера
type Wizard struct {
	app            *tview.Application
	pages          *tview.Pages
	rootDir        string
	initWizard     initwizard.Wizard
	nodeManager    *NodeManager
	templateManager *TemplateManager
}

// NewWizard создает новый экземпляр интерактивного мастера
func NewWizard(rootDir string) *Wizard {
	app := tview.NewApplication()
	pages := tview.NewPages()

	// Создаем основной init wizard
	initWizard := initwizard.NewInitWizard(rootDir)
	
	// Создаем менеджер узлов
	nodeManager := NewNodeManager(rootDir)
	
	// Создаем менеджер шаблонов
	templateManager := NewTemplateManager(rootDir)

	return &Wizard{
		app:            app,
		pages:          pages,
		rootDir:        rootDir,
		initWizard:     initWizard,
		nodeManager:    nodeManager,
		templateManager: templateManager,
	}
}

// Run запускает интерактивный мастер
func (w *Wizard) Run() error {
	// Настраиваем обработчики клавиш
	w.setupInputCapture()

	// Создаем главное меню
	w.showMainMenu()

	// Запускаем приложение
	return w.app.Run()
}

// setupInputCapture настраивает обработку ввода
func (w *Wizard) setupInputCapture() {
	w.app.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		switch event.Key() {
		case tcell.KeyCtrlC, tcell.KeyEscape:
			// Если мы на главной странице, выходим
			if w.pages.GetPageCount() == 1 {
				w.app.Stop()
				return nil
			}
			// Иначе возвращаемся назад
			w.pages.HidePage("main")
			w.pages.ShowPage("menu")
			return nil
		case tcell.KeyCtrlQ:
			w.app.Stop()
			return nil
		}
		return event
	})
}

// showMainMenu показывает главное меню
func (w *Wizard) showMainMenu() {
	flex := tview.NewFlex().
		SetDirection(tview.FlexRow).
		AddItem(tview.NewBox(), 3, 0, false).
		AddItem(w.createMainMenu(), 0, 1, true).
		AddItem(tview.NewBox(), 3, 0, false)

	w.pages.AddAndSwitchToPage("menu", flex, true)
}

// createMainMenu создает главное меню
func (w *Wizard) createMainMenu() *tview.Flex {
	menu := tview.NewGrid().
		SetColumns(0, 40, 0).
		SetRows(0, 3, 1, 3, 1, 3, 1, 3, 1, 3, 0)

	title := tview.NewTextView().
		SetTextAlign(tview.AlignCenter).
		SetText("[yellow]TALM - Интерактивный режим[-]")

	menu.AddItem(title, 1, 1, 1, 1, 0, 0, false)

	// Проверяем, существует ли проект
	isProjectExists := w.checkProjectExists()

	// Кнопки меню
	buttons := []struct {
		text     string
		row      int
		action   func()
		disabled bool
	}{
		{"1. Инициализация проекта (talm init)", 2, w.startInitWizard, false},
		{"2. Получение информации об узлах", 4, w.showNodesInfo, !isProjectExists},
		{"3. Генерация шаблонов (talm template)", 6, w.showTemplateWizard, !isProjectExists},
		{"4. Выход", 8, func() { w.app.Stop() }, false},
	}

	for i, btn := range buttons {
		button := tview.NewButton(btn.text).
			SetSelectedFunc(btn.action)

		if btn.disabled {
			button.SetDisabled(true)
		}

		menu.AddItem(button, btn.row, 1, 1, 1, 0, 0, i == 0)
	}

	flex := tview.NewFlex().
		SetDirection(tview.FlexRow).
		AddItem(menu, 0, 1, true)

	return flex
}

// checkProjectExists проверяет, существует ли проект
func (w *Wizard) checkProjectExists() bool {
	chartFile := filepath.Join(w.rootDir, "Chart.yaml")
	_, err := os.Stat(chartFile)
	return err == nil
}

// startInitWizard запускает мастер инициализации
func (w *Wizard) startInitWizard() {
	w.pages.HidePage("menu")
	
	// Запускаем init wizard
	go func() {
		if err := w.initWizard.Run(); err != nil {
			w.showErrorModal(fmt.Sprintf("Ошибка инициализации: %v", err))
		} else {
			w.showSuccessModal("Проект успешно инициализирован!")
		}
		w.pages.ShowPage("menu")
	}()
}

// showNodesInfo показывает информацию об узлах
func (w *Wizard) showNodesInfo() {
	w.pages.HidePage("menu")
	
	// Создаем страницу информации об узлах
	w.createNodesInfoPage()
	
	// Загружаем информацию об узлах
	go w.loadNodesInfo()
}

// createNodesInfoPage создает страницу информации об узлах
func (w *Wizard) createNodesInfoPage() {
	flex := tview.NewFlex().
		SetDirection(tview.FlexRow).
		AddItem(tview.NewBox(), 1, 0, false).
		AddItem(w.createNodesInfoContent(), 0, 1, true).
		AddItem(tview.NewBox(), 1, 0, false)

	w.pages.AddAndSwitchToPage("nodes_info", flex, true)
}

// createNodesInfoContent создает содержимое страницы информации об узлах
func (w *Wizard) createNodesInfoContent() *tview.Flex {
	content := tview.NewGrid().
		SetColumns(0, 60, 0).
		SetRows(0, 3, 1, 3, 1, 3, 0)

	title := tview.NewTextView().
		SetTextAlign(tview.AlignCenter).
		SetText("[yellow]Информация об узлах[-]")

	content.AddItem(title, 1, 1, 1, 1, 0, 0, false)

	// Список команд для получения информации
	commands := []string{
		"version - Версия Talos",
		"list - Список файлов",
		"memory - Информация о памяти",
		"processes - Список процессов",
		"mounts - Список монтирований",
		"disks - Информация о дисках",
		"netstat - Сетевые соединения",
		"health - Состояние кластера",
		"support - Поддержка и отладка",
	}

	infoText := "Выберите команду для получения информации об узлах:\n\n"
	for i, cmd := range commands {
		infoText += fmt.Sprintf("%d. %s\n", i+1, cmd)
	}

	infoView := tview.NewTextView().
		SetText(infoText).
		SetScrollable(true)

	content.AddItem(infoView, 2, 1, 1, 1, 0, 0, false)

	// Кнопки навигации
	backButton := tview.NewButton("Назад").
		SetSelectedFunc(func() {
			w.pages.HidePage("nodes_info")
			w.pages.ShowPage("menu")
		})

	content.AddItem(backButton, 4, 1, 1, 1, 0, 0, false)

	box := tview.NewFlex().
		SetDirection(tview.FlexRow).
		AddItem(content, 0, 1, true)

	return box
}

// loadNodesInfo загружает информацию об узлах
func (w *Wizard) loadNodesInfo() {
	// Загружаем узлы из конфигурации
	if err := w.nodeManager.LoadNodes(); err != nil {
		w.pages.HidePage("nodes_info")
		w.showErrorModal(fmt.Sprintf("Ошибка загрузки узлов: %v", err))
		w.pages.ShowPage("menu")
		return
	}

	// Обновляем страницу с информацией об узлах
	w.updateNodesInfoPage()

	// Показываем обновленную страницу
	w.pages.ShowPage("nodes_info")
}

// updateNodesInfoPage обновляет страницу информации об узлах
func (w *Wizard) updateNodesInfoPage() {
	nodes := w.nodeManager.GetNodes()
	
	// Обновляем контент страницы
	flex := tview.NewFlex().
		SetDirection(tview.FlexRow).
		AddItem(tview.NewBox(), 1, 0, false).
		AddItem(w.createNodesInfoContentWithNodes(nodes), 0, 1, true).
		AddItem(tview.NewBox(), 1, 0, false)

	w.pages.AddAndSwitchToPage("nodes_info", flex, true)
}

// createNodesInfoContentWithNodes создает содержимое страницы с информацией об узлах
func (w *Wizard) createNodesInfoContentWithNodes(nodes []NodeInfo) *tview.Flex {
	content := tview.NewGrid().
		SetColumns(0, 60, 0).
		SetRows(0, 3, 1, 3, 1, 3, 0)

	title := tview.NewTextView().
		SetTextAlign(tview.AlignCenter).
		SetText("[yellow]Информация об узлах[-]")

	content.AddItem(title, 1, 1, 1, 1, 0, 0, false)

	// Список узлов
	if len(nodes) == 0 {
		noNodesText := tview.NewTextView().
			SetText("Узлы не найдены.\n\nВозможные причины:\n• Проект не инициализирован\n• Нет файлов конфигурации узлов\n• Ошибка чтения конфигурации").
			SetTextAlign(tview.AlignCenter)

		content.AddItem(noNodesText, 2, 1, 1, 1, 0, 0, false)
	} else {
		// Отображаем список узлов
		nodesText := "Найденные узлы:\n\n"
		for i, node := range nodes {
			nodesText += fmt.Sprintf("%d. %s\n", i+1, node.Hostname)
			if node.IP != "" {
				nodesText += fmt.Sprintf("   IP: %s\n", node.IP)
			}
			nodesText += fmt.Sprintf("   Статус: %s\n\n", node.Status)
		}

		nodesView := tview.NewTextView().
			SetText(nodesText).
			SetScrollable(true)

		content.AddItem(nodesView, 2, 1, 1, 1, 0, 0, false)
	}

	// Команды для работы с узлами
	commandsText := "\nДоступные команды:\n"
	commands := []string{
		"version - Версия Talos",
		"list - Список файлов",
		"memory - Информация о памяти",
		"processes - Список процессов",
		"mounts - Список монтирований",
		"disks - Информация о дисках",
		"health - Состояние кластера",
	}
	
	for i, cmd := range commands {
		commandsText += fmt.Sprintf("%d. %s\n", i+1, cmd)
	}

	commandsView := tview.NewTextView().
		SetText(commandsText).
		SetScrollable(true)

	content.AddItem(commandsView, 4, 1, 1, 1, 0, 0, false)

	// Кнопки навигации
	backButton := tview.NewButton("Назад").
		SetSelectedFunc(func() {
			w.pages.HidePage("nodes_info")
			w.pages.ShowPage("menu")
		})

	content.AddItem(backButton, 6, 1, 1, 1, 0, 0, false)

	flex := tview.NewFlex().
		SetDirection(tview.FlexRow).
		AddItem(content, 0, 1, true)

	return flex
}

// showTemplateWizard показывает мастер шаблонов
func (w *Wizard) showTemplateWizard() {
	w.pages.HidePage("menu")
	
	// Обнаруживаем доступные шаблоны
	if err := w.templateManager.DiscoverTemplates(); err != nil {
		w.showErrorModal(fmt.Sprintf("Ошибка обнаружения шаблонов: %v", err))
		w.pages.ShowPage("menu")
		return
	}
	
	// Создаем страницу мастера шаблонов
	w.createTemplatePage()
}

// createTemplatePage создает страницу мастера шаблонов
func (w *Wizard) createTemplatePage() {
	flex := tview.NewFlex().
		SetDirection(tview.FlexRow).
		AddItem(tview.NewBox(), 1, 0, false).
		AddItem(w.createTemplateContent(), 0, 1, true).
		AddItem(tview.NewBox(), 1, 0, false)

	w.pages.AddAndSwitchToPage("template", flex, true)
}

// createTemplateContent создает содержимое страницы шаблонов
func (w *Wizard) createTemplateContent() *tview.Flex {
	content := tview.NewGrid().
		SetColumns(0, 60, 0).
		SetRows(0, 3, 1, 3, 1, 3, 0)

	title := tview.NewTextView().
		SetTextAlign(tview.AlignCenter).
		SetText("[yellow]Генерация шаблонов (talm template)[-]")

	content.AddItem(title, 1, 1, 1, 1, 0, 0, false)

	// Получаем доступные шаблоны
	templates := w.templateManager.GetTemplateFiles()
	values := w.templateManager.GetValuesFiles()

	infoText := "Мастер генерации шаблонов\n\n"
	
	if len(templates) == 0 {
		infoText += "[red]Шаблоны не найдены![-]\n\n"
		infoText += "Возможные причины:\n"
		infoText += "• Нет директории templates/\n"
		infoText += "• Нет файлов шаблонов\n"
		infoText += "• Ошибка сканирования директорий\n\n"
	} else {
		infoText += fmt.Sprintf("Найдено шаблонов: %d\n", len(templates))
		for i, template := range templates {
			infoText += fmt.Sprintf("%d. %s\n", i+1, template)
		}
		infoText += "\n"
	}

	if len(values) > 0 {
		infoText += fmt.Sprintf("Найдено файлов значений: %d\n", len(values))
		for i, value := range values {
			infoText += fmt.Sprintf("%d. %s\n", i+1, value)
		}
	} else {
		infoText += "Файлы значений не найдены\n"
	}

	infoText += "\n\nДоступные функции:\n"
	infoText += "1. Выбор шаблонов для рендеринга\n"
	infoText += "2. Настройка параметров\n"
	infoText += "3. Генерация конфигураций\n"
	infoText += "4. Просмотр результатов\n"
	infoText += "5. Сохранение в файлы\n"

	infoView := tview.NewTextView().
		SetText(infoText).
		SetScrollable(true)

	content.AddItem(infoView, 2, 1, 1, 1, 0, 0, false)

	// Кнопки действий
	buttons := tview.NewFlex().SetDirection(tview.FlexRow)

	if len(templates) > 0 {
		renderButton := tview.NewButton("1. Рендерить шаблоны").
			SetSelectedFunc(w.renderTemplates)
		buttons.AddItem(renderButton, 1, 0, true)
	}

	showConfigButton := tview.NewButton("2. Показать конфигурацию").
		SetSelectedFunc(w.showTemplateConfig)
	buttons.AddItem(showConfigButton, 1, 0, true)

	// Кнопки навигации
	backButton := tview.NewButton("Назад").
		SetSelectedFunc(func() {
			w.pages.HidePage("template")
			w.pages.ShowPage("menu")
		})
	buttons.AddItem(backButton, 1, 0, true)

	content.AddItem(buttons, 4, 1, 1, 1, 0, 0, false)

	box := tview.NewFlex().
		SetDirection(tview.FlexRow).
		AddItem(content, 0, 1, true)

	return box
}

// renderTemplates выполняет рендеринг шаблонов
func (w *Wizard) renderTemplates() {
	w.pages.HidePage("template")
	
	go func() {
		ctx := context.Background()
		templates := w.templateManager.GetTemplateFiles()
		
		// Если есть шаблоны, рендерим их
		if len(templates) == 0 {
			w.showErrorModal("Нет шаблонов для рендеринга")
			w.pages.ShowPage("template")
			return
		}
		
		// Рендерим все шаблоны
		result, err := w.templateManager.RenderTemplates(ctx, templates, nil)
		if err != nil {
			w.showErrorModal(fmt.Sprintf("Ошибка рендеринга: %v", err))
			w.pages.ShowPage("template")
			return
		}
		
		// Сохраняем результаты
		if err := w.templateManager.SaveRenderedTemplates(result, ""); err != nil {
			w.showErrorModal(fmt.Sprintf("Ошибка сохранения: %v", err))
			w.pages.ShowPage("template")
			return
		}
		
		outputFiles := w.templateManager.GetOutputFiles()
		successMsg := fmt.Sprintf("Шаблоны успешно отрендерены!\n\nСохранено файлов: %d\n\n", len(outputFiles))
		for i, file := range outputFiles {
			successMsg += fmt.Sprintf("%d. %s\n", i+1, file)
		}
		
		w.showSuccessModal(successMsg)
		w.pages.ShowPage("template")
	}()
}

// showTemplateConfig показывает конфигурацию шаблонов
func (w *Wizard) showTemplateConfig() {
	// Показываем диалог с конфигурацией
	infoText := "Конфигурация шаблонов:\n\n"
	
	// Показываем доступные шаблоны
	templates := w.templateManager.GetTemplateFiles()
	infoText += fmt.Sprintf("Шаблоны (%d):\n", len(templates))
	for i, template := range templates {
		infoText += fmt.Sprintf("%d. %s\n", i+1, template)
	}
	infoText += "\n"
	
	// Показываем файлы значений
	values := w.templateManager.GetValuesFiles()
	infoText += fmt.Sprintf("Файлы значений (%d):\n", len(values))
	for i, value := range values {
		infoText += fmt.Sprintf("%d. %s\n", i+1, value)
	}
	infoText += "\n"
	
	infoText += "Параметры рендеринга:\n"
	infoText += "• Offline режим: включен\n"
	infoText += "• Выходная директория: rendered/\n"
	infoText += "• Полный рендеринг: включен\n"
	
	w.showInfoModal(infoText)
}

// showErrorModal показывает модальное окно с ошибкой
func (w *Wizard) showErrorModal(message string) {
	modal := tview.NewModal().
		SetText(message).
		AddButtons([]string{"OK"}).
		SetDoneFunc(func(buttonIndex int, buttonLabel string) {
			w.pages.HidePage("error")
			w.pages.ShowPage("menu")
		})

	w.pages.AddAndSwitchToPage("error", modal, false)
	w.pages.ShowPage("error")
}

// showSuccessModal показывает модальное окно с успехом
func (w *Wizard) showSuccessModal(message string) {
	modal := tview.NewModal().
		SetText(message).
		AddButtons([]string{"OK"}).
		SetDoneFunc(func(buttonIndex int, buttonLabel string) {
			w.pages.HidePage("success")
			w.pages.ShowPage("menu")
		})

	w.pages.AddAndSwitchToPage("success", modal, false)
	w.pages.ShowPage("success")
}

// showInfoModal показывает информационное модальное окно
func (w *Wizard) showInfoModal(message string) {
	modal := tview.NewModal().
		SetText(message).
		AddButtons([]string{"OK"}).
		SetDoneFunc(func(buttonIndex int, buttonLabel string) {
			w.pages.HidePage("info")
			w.pages.ShowPage("menu")
		})

	w.pages.AddAndSwitchToPage("info", modal, false)
	w.pages.ShowPage("info")
}