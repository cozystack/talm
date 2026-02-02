package initwizard

import (
	"fmt"
	"sort"
	"strings"
)

// DataProcessorImpl реализует интерфейс DataProcessor
type DataProcessorImpl struct{}

// NewDataProcessor создает новый экземпляр процессора данных
func NewDataProcessor() DataProcessor {
	return &DataProcessorImpl{}
}

// FilterAndSortNodes фильтрует и сортирует список нод
func (p *DataProcessorImpl) FilterAndSortNodes(nodes []NodeInfo) []NodeInfo {
	if len(nodes) == 0 {
		return nodes
	}

	// Фильтруем ноды
	var filtered []NodeInfo
	for _, node := range nodes {
		// Исключаем ноды без IP адреса
		if node.IP == "" {
			continue
		}

		// Исключаем дубликаты по MAC адресу
		if node.MAC != "" {
			isDuplicate := false
			for _, existing := range filtered {
				if existing.MAC == node.MAC {
					isDuplicate = true
					break
				}
			}
			if isDuplicate {
				continue
			}
		}

		filtered = append(filtered, node)
	}

	// Сортируем ноды: сначала controlplane, затем worker
	sort.Slice(filtered, func(i, j int) bool {
		// Приоритет controlplane над worker
		if filtered[i].Type != filtered[j].Type {
			if filtered[i].Type == "controlplane" {
				return true
			}
			if filtered[j].Type == "controlplane" {
				return false
			}
		}
		// Если типы одинаковые, сортируем по IP
		return filtered[i].IP < filtered[j].IP
	})

	return filtered
}

// ExtractHardwareInfo извлекает и обрабатывает информацию об оборудовании
func (p *DataProcessorImpl) ExtractHardwareInfo(ip string) (Hardware, error) {
	// Этот метод может быть использован для дополнительной обработки
	// информации об оборудовании, если потребуется
	return Hardware{}, fmt.Errorf("метод ExtractHardwareInfo не реализован")
}

// ProcessScanResults обрабатывает результаты сканирования сети
func (p *DataProcessorImpl) ProcessScanResults(results []NodeInfo) []NodeInfo {
	if len(results) == 0 {
		return results
	}

	// Удаляем дубликаты
	processed := p.RemoveDuplicatesByMAC(results)

	// Фильтруем и сортируем
	processed = p.FilterAndSortNodes(processed)

	return processed
}

// CalculateResourceStats вычисляет статистику ресурсов ноды
func (p *DataProcessorImpl) CalculateResourceStats(node NodeInfo) (cpu, ram, disks int) {
	// Подсчет CPU
	cpu = node.CPU
	if cpu == 0 {
		// Пытаемся подсчитать из информации об оборудовании
		for _, processor := range node.Hardware.Processors {
			cpu += processor.ThreadCount
		}
	}

	// Подсчет RAM
	ram = node.RAM
	if ram == 0 {
		// Подсчитываем из информации об оборудовании
		ram = node.Hardware.Memory.Size / 1024 // MiB to GiB
	}

	// Подсчет дисков
	disks = len(node.Disks)
	if disks == 0 {
		disks = len(node.Hardware.Blockdevices)
	}

	return cpu, ram, disks
}

// RemoveDuplicatesByMAC удаляет дубликаты нод по MAC адресу
func (p *DataProcessorImpl) RemoveDuplicatesByMAC(nodes []NodeInfo) []NodeInfo {
	if len(nodes) == 0 {
		return nodes
	}

	seen := make(map[string]bool)
	var unique []NodeInfo

	for _, node := range nodes {
		mac := node.MAC
		if mac == "" {
			// Если MAC пустой, добавляем по IP как fallback
			unique = append(unique, node)
			continue
		}

		if !seen[mac] {
			seen[mac] = true
			unique = append(unique, node)
		}
	}

	return unique
}

// FilterNodesByRole фильтрует ноды по роли
func (p *DataProcessorImpl) FilterNodesByRole(nodes []NodeInfo, role string) []NodeInfo {
	var filtered []NodeInfo
	
	for _, node := range nodes {
		if node.Type == role {
			filtered = append(filtered, node)
		}
	}
	
	return filtered
}

// FilterNodesByManufacturer фильтрует ноды по производителю
func (p *DataProcessorImpl) FilterNodesByManufacturer(nodes []NodeInfo, manufacturer string) []NodeInfo {
	var filtered []NodeInfo
	
	for _, node := range nodes {
		if strings.Contains(strings.ToLower(node.Manufacturer), strings.ToLower(manufacturer)) {
			filtered = append(filtered, node)
		}
	}
	
	return filtered
}

// SortNodesByCPU сортирует ноды по количеству CPU ядер
func (p *DataProcessorImpl) SortNodesByCPU(nodes []NodeInfo, ascending bool) []NodeInfo {
	sorted := make([]NodeInfo, len(nodes))
	copy(sorted, nodes)
	
	sort.Slice(sorted, func(i, j int) bool {
		cpuI, _, _ := p.CalculateResourceStats(sorted[i])
		cpuJ, _, _ := p.CalculateResourceStats(sorted[j])
		
		if ascending {
			return cpuI < cpuJ
		}
		return cpuI > cpuJ
	})
	
	return sorted
}

// SortNodesByRAM сортирует ноды по объему RAM
func (p *DataProcessorImpl) SortNodesByRAM(nodes []NodeInfo, ascending bool) []NodeInfo {
	sorted := make([]NodeInfo, len(nodes))
	copy(sorted, nodes)
	
	sort.Slice(sorted, func(i, j int) bool {
		_, ramI, _ := p.CalculateResourceStats(sorted[i])
		_, ramJ, _ := p.CalculateResourceStats(sorted[j])
		
		if ascending {
			return ramI < ramJ
		}
		return ramI > ramJ
	})
	
	return sorted
}

// SortNodesByDisks сортирует ноды по количеству дисков
func (p *DataProcessorImpl) SortNodesByDisks(nodes []NodeInfo, ascending bool) []NodeInfo {
	sorted := make([]NodeInfo, len(nodes))
	copy(sorted, nodes)
	
	sort.Slice(sorted, func(i, j int) bool {
		_, _, disksI := p.CalculateResourceStats(sorted[i])
		_, _, disksJ := p.CalculateResourceStats(sorted[j])
		
		if ascending {
			return disksI < disksJ
		}
		return disksI > disksJ
	})
	
	return sorted
}

// GetNodeSummary возвращает сводную информацию о ноде
func (p *DataProcessorImpl) GetNodeSummary(node NodeInfo) string {
	cpu, ram, disks := p.CalculateResourceStats(node)
	
	summary := fmt.Sprintf("IP: %s", node.IP)
	
	if node.Hostname != "" && node.Hostname != node.IP {
		summary += fmt.Sprintf(", Hostname: %s", node.Hostname)
	}
	
	if node.Manufacturer != "" {
		summary += fmt.Sprintf(", CPU: %s", node.Manufacturer)
	}
	
	if cpu > 0 {
		summary += fmt.Sprintf(" %d cores", cpu)
	}
	
	if ram > 0 {
		summary += fmt.Sprintf(", RAM: %d GB", ram)
	}
	
	if disks > 0 {
		summary += fmt.Sprintf(", Disks: %d", disks)
	}
	
	if node.Type != "" {
		summary += fmt.Sprintf(", Role: %s", node.Type)
	}
	
	return summary
}

// GetClusterSummary возвращает сводную информацию о кластере нод
func (p *DataProcessorImpl) GetClusterSummary(nodes []NodeInfo) string {
	if len(nodes) == 0 {
		return "Нет доступных нод"
	}
	
	controlplaneCount := 0
	workerCount := 0
	totalCPU := 0
	totalRAM := 0
	totalDisks := 0
	
	for _, node := range nodes {
		if node.Type == "controlplane" {
			controlplaneCount++
		} else if node.Type == "worker" {
			workerCount++
		}
		
		cpu, ram, disks := p.CalculateResourceStats(node)
		totalCPU += cpu
		totalRAM += ram
		totalDisks += disks
	}
	
	summary := fmt.Sprintf("Всего нод: %d", len(nodes))
	if controlplaneCount > 0 {
		summary += fmt.Sprintf(", Control Plane: %d", controlplaneCount)
	}
	if workerCount > 0 {
		summary += fmt.Sprintf(", Worker: %d", workerCount)
	}
	summary += fmt.Sprintf(", CPU: %d cores, RAM: %d GB, Disks: %d", totalCPU, totalRAM, totalDisks)
	
	return summary
}

// ValidateNodeCompatibility проверяет совместимость нод для кластера
func (p *DataProcessorImpl) ValidateNodeCompatibility(nodes []NodeInfo) error {
	if len(nodes) == 0 {
		return fmt.Errorf("нет нод для проверки совместимости")
	}
	
	// Проверяем наличие хотя бы одной controlplane ноды
	hasControlplane := false
	for _, node := range nodes {
		if node.Type == "controlplane" {
			hasControlplane = true
			break
		}
	}
	
	if !hasControlplane {
		return fmt.Errorf("в кластере должна быть хотя бы одна controlplane нода")
	}
	
	// Проверяем уникальность MAC адресов
	macSet := make(map[string]bool)
	for _, node := range nodes {
		if node.MAC != "" {
			if macSet[node.MAC] {
				return fmt.Errorf("обнаружен дубликат MAC адреса: %s", node.MAC)
			}
			macSet[node.MAC] = true
		}
	}
	
	return nil
}

// GroupNodesByType группирует ноды по типу
func (p *DataProcessorImpl) GroupNodesByType(nodes []NodeInfo) map[string][]NodeInfo {
	groups := make(map[string][]NodeInfo)
	
	for _, node := range nodes {
		nodeType := node.Type
		if nodeType == "" {
			nodeType = "unknown"
		}
		groups[nodeType] = append(groups[nodeType], node)
	}
	
	return groups
}

// FindBestControlplaneNode находит лучшую ноду для роли controlplane
func (p *DataProcessorImpl) FindBestControlplaneNode(nodes []NodeInfo) (NodeInfo, error) {
	var controlplaneNodes []NodeInfo
	
	for _, node := range nodes {
		if node.Type == "controlplane" {
			controlplaneNodes = append(controlplaneNodes, node)
		}
	}
	
	if len(controlplaneNodes) == 0 {
		return NodeInfo{}, fmt.Errorf("нет доступных controlplane нод")
	}
	
	// Сортируем по ресурсам (больше CPU и RAM = лучше)
	sort.Slice(controlplaneNodes, func(i, j int) bool {
		cpuI, ramI, _ := p.CalculateResourceStats(controlplaneNodes[i])
		cpuJ, ramJ, _ := p.CalculateResourceStats(controlplaneNodes[j])
		
		// Сравниваем по CPU, затем по RAM
		if cpuI != cpuJ {
			return cpuI > cpuJ
		}
		return ramI > ramJ
	})
	
	return controlplaneNodes[0], nil
}