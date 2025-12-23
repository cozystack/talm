package initwizard

import (
	"fmt"
	"net"
	"regexp"
	"strings"
)

// ValidatorImpl реализует интерфейс Validator
type ValidatorImpl struct{}

// NewValidator создает новый экземпляр валидатора
func NewValidator() Validator {
	return &ValidatorImpl{}
}

// ValidateNetworkCIDR проверяет корректность CIDR нотации сети
func (v *ValidatorImpl) ValidateNetworkCIDR(cidr string) error {
	if strings.TrimSpace(cidr) == "" {
		return NewValidationError(
			"VAL_001", 
			"сеть для сканирования не может быть пустой", 
			"поле CIDR сети обязательно для сканирования",
		)
	}

	_, _, err := net.ParseCIDR(cidr)
	if err != nil {
		return NewValidationErrorWithCause(
			"VAL_002", 
			"некорректная CIDR нотация", 
			fmt.Sprintf("предоставленная CIDR: %s", cidr), 
			err,
		)
	}

	return nil
}

// ValidateClusterName проверяет корректность имени кластера
func (v *ValidatorImpl) ValidateClusterName(name string) error {
	if strings.TrimSpace(name) == "" {
		return NewValidationError(
			"VAL_003", 
			"имя кластера не может быть пустым", 
			"поле имени кластера обязательно",
		)
	}

	// Проверяем, что имя содержит только допустимые символы
	validName := regexp.MustCompile(`^[a-z0-9-]+$`)
	if !validName.MatchString(name) {
		return NewValidationError(
			"VAL_004", 
			"имя кластера может содержать только строчные буквы, цифры и дефисы", 
			fmt.Sprintf("предоставленное имя: %s", name),
		)
	}

	// Проверяем длину имени
	if len(name) > 50 {
		return NewValidationError(
			"VAL_005", 
			"имя кластера не должно превышать 50 символов", 
			fmt.Sprintf("текущая длина: %d", len(name)),
		)
	}

	return nil
}

// ValidateHostname проверяет корректность имени хоста
func (v *ValidatorImpl) ValidateHostname(hostname string) error {
	if strings.TrimSpace(hostname) == "" {
		return NewValidationError(
			"VAL_006", 
			"имя хоста не может быть пустым", 
			"поле имени хоста обязательно",
		)
	}

	// Проверяем, что hostname соответствует RFC стандарту
	validHostname := regexp.MustCompile(`^[a-zA-Z0-9]([a-zA-Z0-9\-]{0,61}[a-zA-Z0-9])?(\.[a-zA-Z0-9]([a-zA-Z0-9\-]{0,61}[a-zA-Z0-9])?)*$`)
	if !validHostname.MatchString(hostname) {
		return NewValidationError(
			"VAL_007", 
			"некорректное имя хоста", 
			fmt.Sprintf("предоставленное имя хоста: %s", hostname),
		)
	}

	return nil
}

// ValidateRequiredField проверяет, что обязательное поле не пустое
func (v *ValidatorImpl) ValidateRequiredField(value, fieldName string) error {
	if strings.TrimSpace(value) == "" {
		return NewValidationError(
			"VAL_008", 
			fmt.Sprintf("поле '%s' является обязательным", fieldName), 
			"значение поля не должно быть пустым",
		)
	}
	return nil
}

// ValidateIP проверяет корректность IP адреса
func (v *ValidatorImpl) ValidateIP(ip string) error {
	if strings.TrimSpace(ip) == "" {
		return NewValidationError(
			"VAL_009", 
			"IP адрес не может быть пустым", 
			"поле IP адреса обязательно",
		)
	}

	if parsedIP := net.ParseIP(ip); parsedIP == nil {
		return NewValidationError(
			"VAL_010", 
			"некорректный IP адрес", 
			fmt.Sprintf("предоставленный IP: %s", ip),
		)
	}

	return nil
}

// ValidateVIP проверяет корректность виртуального IP адреса
func (v *ValidatorImpl) ValidateVIP(vip string) error {
	if strings.TrimSpace(vip) == "" {
		// VIP опциональный, пустая строка допустима
		return nil
	}

	return v.ValidateIP(vip)
}

// ValidateDNSservers проверяет корректность списка DNS серверов
func (v *ValidatorImpl) ValidateDNSservers(dns string) error {
	if strings.TrimSpace(dns) == "" {
		return NewValidationError(
			"VAL_011", 
			"DNS серверы не могут быть пустыми", 
			"необходимо указать хотя бы один DNS сервер",
		)
	}

	// Разделяем список DNS серверов
	dnsServers := strings.Split(dns, ",")

	var invalidServers []string
	for _, server := range dnsServers {
		server = strings.TrimSpace(server)
		if server == "" {
			continue
		}

		// Проверяем каждый DNS сервер
		if err := v.ValidateIP(server); err != nil {
			invalidServers = append(invalidServers, server)
		}
	}

	if len(invalidServers) > 0 {
		return NewValidationError(
			"VAL_012", 
			"обнаружены некорректные DNS серверы", 
			fmt.Sprintf("некорректные серверы: %v", invalidServers),
		)
	}

	return nil
}

// ValidateNetworkConfig проверяет корректность сетевой конфигурации
func (v *ValidatorImpl) ValidateNetworkConfig(addresses, gateway, dnsServers string) error {
	// Проверяем адреса
	if err := v.ValidateRequiredField(addresses, "Addresses"); err != nil {
		return err
	}

	// Проверяем шлюз
	if err := v.ValidateRequiredField(gateway, "Gateway"); err != nil {
		return err
	}

	// Проверяем DNS серверы
	if err := v.ValidateDNSservers(dnsServers); err != nil {
		return err
	}

	return nil
}

// ValidateNodeType проверяет корректность типа ноды
func (v *ValidatorImpl) ValidateNodeType(nodeType string) error {
	validTypes := []string{"controlplane", "worker", "control-plane"}

	for _, validType := range validTypes {
		if nodeType == validType {
			return nil
		}
	}

	return NewValidationError(
		"VAL_013", 
		"некорректный тип ноды", 
		fmt.Sprintf("тип: %s, допустимые значения: %v", nodeType, validTypes),
	)
}

// ValidatePreset проверяет корректность пресета
func (v *ValidatorImpl) ValidatePreset(preset string) error {
	validPresets := []string{"generic", "cozystack"}

	for _, validPreset := range validPresets {
		if preset == validPreset {
			return nil
		}
	}

	return NewValidationError(
		"VAL_014", 
		"некорректный пресет", 
		fmt.Sprintf("пресет: %s, допустимые значения: %v", preset, validPresets),
	)
}

// ValidateAPIServerURL проверяет корректность URL API сервера
func (v *ValidatorImpl) ValidateAPIServerURL(url string) error {
	if strings.TrimSpace(url) == "" {
		return NewValidationError(
			"VAL_015", 
			"URL API сервера не может быть пустым", 
			"необходимо указать URL API сервера кластера",
		)
	}

	// Проверяем базовый формат URL
	if !strings.HasPrefix(url, "https://") && !strings.HasPrefix(url, "http://") {
		return NewValidationError(
			"VAL_016", 
			"URL API сервера должен начинаться с http:// или https://", 
			fmt.Sprintf("предоставленный URL: %s", url),
		)
	}

	// Проверяем, что URL содержит порт
	if !strings.Contains(url, ":") {
		return NewValidationError(
			"VAL_017", 
			"URL API сервера должен содержать порт (например, :6443)", 
			fmt.Sprintf("предоставленный URL: %s", url),
		)
	}

	return nil
}