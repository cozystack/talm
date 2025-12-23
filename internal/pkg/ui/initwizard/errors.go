package initwizard

import (
	"fmt"
	"strings"
)

// ErrorType определяет тип ошибки для программной обработки
type ErrorType int

const (
	// Ошибки валидации
	ErrValidation ErrorType = iota + 1000
	// Ошибки сети
	ErrNetwork
	// Ошибки файловой системы
	ErrFilesystem
	// Ошибки конфигурации
	ErrConfiguration
	// Ошибки генерации
	ErrGeneration
	// Ошибки сканирования
	ErrScanning
	// Ошибки обработки данных
	ErrDataProcessing
	// Ошибки UI
	ErrUI
	// Внутренние ошибки
	ErrInternal
)

// AppError представляет ошибку с контекстом и кодом
type AppError struct {
	Type       ErrorType
	Code       string
	Message    string
	Details    string
	Original   error
	Location   string
	StackTrace []string
}

// NewError создает новую ошибку приложения
func NewError(errorType ErrorType, code, message, details string) *AppError {
	return &AppError{
		Type:     errorType,
		Code:     code,
		Message:  message,
		Details:  details,
		Location: getCallerLocation(),
	}
}

// NewErrorWithCause создает новую ошибку с исходной причиной
func NewErrorWithCause(errorType ErrorType, code, message, details string, original error) *AppError {
	err := NewError(errorType, code, message, details)
	err.Original = original
	return err
}

// Error реализует интерфейс error
func (e *AppError) Error() string {
	var result strings.Builder
	
	// Базовое сообщение
	result.WriteString(fmt.Sprintf("[%s] %s", e.Code, e.Message))
	
	// Детали если есть
	if e.Details != "" {
		result.WriteString(fmt.Sprintf(": %s", e.Details))
	}
	
	// Местоположение
	if e.Location != "" {
		result.WriteString(fmt.Sprintf(" (location: %s)", e.Location))
	}
	
	// Исходная ошибка если есть
	if e.Original != nil {
		result.WriteString(fmt.Sprintf("; caused by: %v", e.Original))
	}
	
	return result.String()
}

// Unwrap возвращает исходную ошибку для error wrapping
func (e *AppError) Unwrap() error {
	return e.Original
}

// Is проверяет тип ошибки
func (e *AppError) Is(target error) bool {
	if appErr, ok := target.(*AppError); ok {
		return e.Type == appErr.Type || e.Code == appErr.Code
	}
	return false
}

// IsType проверяет тип ошибки
func (e *AppError) IsType(errorType ErrorType) bool {
	return e.Type == errorType
}

// IsCode проверяет код ошибки
func (e *AppError) IsCode(code string) bool {
	return e.Code == code
}

// WithDetails добавляет дополнительные детали к ошибке
func (e *AppError) WithDetails(details string) *AppError {
	e.Details = details
	return e
}

// WithLocation устанавливает местоположение ошибки
func (e *AppError) WithLocation(location string) *AppError {
	e.Location = location
	return e
}

// Helper функции для создания типизированных ошибок

// NewValidationError создает ошибку валидации
func NewValidationError(code, message, details string) *AppError {
	return NewError(ErrValidation, code, message, details)
}

// NewValidationErrorWithCause создает ошибку валидации с причиной
func NewValidationErrorWithCause(code, message, details string, original error) *AppError {
	return NewErrorWithCause(ErrValidation, code, message, details, original)
}

// NewNetworkError создает ошибку сети
func NewNetworkError(code, message, details string) *AppError {
	return NewError(ErrNetwork, code, message, details)
}

// NewNetworkErrorWithCause создает ошибку сети с причиной
func NewNetworkErrorWithCause(code, message, details string, original error) *AppError {
	return NewErrorWithCause(ErrNetwork, code, message, details, original)
}

// NewFilesystemError создает ошибку файловой системы
func NewFilesystemError(code, message, details string) *AppError {
	return NewError(ErrFilesystem, code, message, details)
}

// NewFilesystemErrorWithCause создает ошибку файловой системы с причиной
func NewFilesystemErrorWithCause(code, message, details string, original error) *AppError {
	return NewErrorWithCause(ErrFilesystem, code, message, details, original)
}

// NewConfigurationError создает ошибку конфигурации
func NewConfigurationError(code, message, details string) *AppError {
	return NewError(ErrConfiguration, code, message, details)
}

// NewConfigurationErrorWithCause создает ошибку конфигурации с причиной
func NewConfigurationErrorWithCause(code, message, details string, original error) *AppError {
	return NewErrorWithCause(ErrConfiguration, code, message, details, original)
}

// NewGenerationError создает ошибку генерации
func NewGenerationError(code, message, details string) *AppError {
	return NewError(ErrGeneration, code, message, details)
}

// NewGenerationErrorWithCause создает ошибку генерации с причиной
func NewGenerationErrorWithCause(code, message, details string, original error) *AppError {
	return NewErrorWithCause(ErrGeneration, code, message, details, original)
}

// NewScanningError создает ошибку сканирования
func NewScanningError(code, message, details string) *AppError {
	return NewError(ErrScanning, code, message, details)
}

// NewScanningErrorWithCause создает ошибку сканирования с причиной
func NewScanningErrorWithCause(code, message, details string, original error) *AppError {
	return NewErrorWithCause(ErrScanning, code, message, details, original)
}

// NewDataProcessingError создает ошибку обработки данных
func NewDataProcessingError(code, message, details string) *AppError {
	return NewError(ErrDataProcessing, code, message, details)
}

// NewDataProcessingErrorWithCause создает ошибку обработки данных с причиной
func NewDataProcessingErrorWithCause(code, message, details string, original error) *AppError {
	return NewErrorWithCause(ErrDataProcessing, code, message, details, original)
}

// NewUIError создает ошибку UI
func NewUIError(code, message, details string) *AppError {
	return NewError(ErrUI, code, message, details)
}

// NewUIErrorWithCause создает ошибку UI с причиной
func NewUIErrorWithCause(code, message, details string, original error) *AppError {
	return NewErrorWithCause(ErrUI, code, message, details, original)
}

// NewInternalError создает внутреннюю ошибку
func NewInternalError(code, message, details string) *AppError {
	return NewError(ErrInternal, code, message, details)
}

// NewInternalErrorWithCause создает внутреннюю ошибку с причиной
func NewInternalErrorWithCause(code, message, details string, original error) *AppError {
	return NewErrorWithCause(ErrInternal, code, message, details, original)
}

// getCallerLocation возвращает местоположение вызова функции
func getCallerLocation() string {
	// Упрощенная версия для получения местоположения
	// В реальном приложении можно использовать более продвинутые методы
	return "initwizard"
}

// WrapError оборачивает существующую ошибку в AppError
func WrapError(err error, errorType ErrorType, code, message, details string) *AppError {
	if appErr, ok := err.(*AppError); ok {
		return appErr
	}
	return NewErrorWithCause(errorType, code, message, details, err)
}

// IsValidationError проверяет, является ли ошибка ошибкой валидации
func IsValidationError(err error) bool {
	if appErr, ok := err.(*AppError); ok {
		return appErr.IsType(ErrValidation)
	}
	return false
}

// IsNetworkError проверяет, является ли ошибка ошибкой сети
func IsNetworkError(err error) bool {
	if appErr, ok := err.(*AppError); ok {
		return appErr.IsType(ErrNetwork)
	}
	return false
}

// IsFilesystemError проверяет, является ли ошибка ошибкой файловой системы
func IsFilesystemError(err error) bool {
	if appErr, ok := err.(*AppError); ok {
		return appErr.IsType(ErrFilesystem)
	}
	return false
}