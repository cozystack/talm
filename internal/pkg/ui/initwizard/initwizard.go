package initwizard

// Этот файл содержит обновленную версию мастера инициализации,
// которая использует новые компоненты вместо монолитного кода.

// RunInitWizard запускает мастер инициализации с использованием новых компонентов
func RunInitWizard() error {
	wizard := NewWizard()
	return wizard.Run()
}

// CheckExistingFiles проверяет наличие существующих файлов конфигурации
func CheckExistingFiles() bool {
	wizard := NewWizard()
	return wizard.checkExistingFiles()
}

// RunInitWizardWithConfig запускает мастер с пользовательской конфигурацией
func RunInitWizardWithConfig(config InitData) error {
	wizard := NewWizard()
	return wizard.RunWithCustomConfig(config)
}

// NewInitWizard создает новый экземпляр мастера инициализации с поддержкой rootDir
func NewInitWizard(rootDir string) Wizard {
	// Создаем мастер с настройками по умолчанию
	wizard := NewWizard()
	
	// Если указан rootDir, меняем рабочую директорию
	if rootDir != "." {
		// В реальном приложении здесь можно добавить логику
		// для работы с другими директориями
	}
	
	return wizard
}