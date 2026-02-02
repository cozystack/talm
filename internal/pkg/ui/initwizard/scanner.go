package initwizard

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"time"
)

// NodeCommandExecutor интерфейс для выполнения команд на узлах
type NodeCommandExecutor interface {
	ExecuteNodeCommand(ctx context.Context, nodeIP, command string) (string, error)
}

// NetworkScannerImpl implements the NetworkScanner interface
type NetworkScannerImpl struct {
	timeout         time.Duration
	commandExecutor NodeCommandExecutor
}

// NewNetworkScanner creates a new network scanner instance
func NewNetworkScanner(commandExecutor NodeCommandExecutor) NetworkScanner {
	return &NetworkScannerImpl{
		timeout:         2 * time.Second,
		commandExecutor: commandExecutor,
	}
}

// ScanNetwork сканирует сеть для обнаружения нод Talos
func (s *NetworkScannerImpl) ScanNetwork(ctx context.Context, cidr string) ([]NodeInfo, error) {
	log.Printf("[DIAGNOSTIC] Starting network scan for CIDR: %s", cidr)
	start := time.Now()
	log.Printf("[DIAGNOSTIC] Контекст отмены: %v", ctx.Err())

	// Получаем список IP адресов с открытым портом 50000
	log.Printf("[DIAGNOSTIC] Начинаем сканирование nmap...")
	nmapStart := time.Now()
	ips, err := s.scanForTalOSNodes(ctx, cidr)
	nmapDuration := time.Since(nmapStart)
	log.Printf("[DIAGNOSTIC] nmap завершен за %v, найдено %d IP адресов: %v", nmapDuration, len(ips), ips)
	if err != nil {
		return nil, fmt.Errorf("сканирование сети не удалось: %v", err)
	}

	if len(ips) == 0 {
		return nil, fmt.Errorf("в сети не найдено нод с открытым портом 50000")
	}

	// Собираем информацию о найденных нодах
	nodes, err := s.ParallelScan(ctx, ips)
	if err != nil {
		return nil, fmt.Errorf("сбор информации о нодах не удался: %v", err)
	}

	log.Printf("Сканирование завершено за %v, найдено %d нод", time.Since(start), len(nodes))
	return nodes, nil
}

// ScanNetworkWithProgress сканирует сеть с отображением прогресса
func (s *NetworkScannerImpl) ScanNetworkWithProgress(ctx context.Context, cidr string, progressFunc func(int)) ([]NodeInfo, error) {
	log.Printf("[FIXED] Запуск сканирования сети с прогрессом для CIDR: %s", cidr)

	// Функция обновления прогресса с heartbeat
	updateProgress := func(stage string, percent int) {
		log.Printf("[FIXED] Прогресс %s: %d%%", stage, percent)
		if progressFunc != nil {
			log.Printf("[FIXED] Вызов progressFunc с %d%%", percent)
			progressFunc(percent)
		} else {
			log.Printf("[FIXED] progressFunc == nil!")
		}
	}

	updateProgress("Начало", 5)

	// Scan the network с heartbeat
	heartbeatCtx, heartbeatCancel := context.WithCancel(ctx)
	defer heartbeatCancel()

	// Запускаем heartbeat для обновления прогресса
	go func() {
		ticker := time.NewTicker(2 * time.Second)
		defer ticker.Stop()

		for {
			select {
			case <-heartbeatCtx.Done():
				return
			case <-ticker.C:
				log.Printf("[FIXED] Heartbeat: сканирование в процессе...")
				if progressFunc != nil {
					// Обновляем прогресс только если он не слишком высокий
					// (избегаем превышения 90% до завершения)
					// progressFunc(15) // Обновляем только для heartbeat
				}
			}
		}
	}()

	updateProgress("Сканирование nmap", 10)

	// Scan the network
	ips, err := s.scanForTalOSNodes(ctx, cidr)
	if err != nil {
		heartbeatCancel()
		return nil, fmt.Errorf("сканирование сети не удалось: %v", err)
	}

	heartbeatCancel() // Останавливаем heartbeat
	updateProgress("Nmap завершен", 20)

	if len(ips) == 0 {
		return nil, fmt.Errorf("в сети не найдено нод с открытым портом 50000")
	}

	log.Printf("[FIXED] Найдено %d IP адресов, начинаем параллельное сканирование", len(ips))

	// Параллельно собираем информацию о нодах
	nodes, err := s.parallelScanWithProgress(ctx, ips, progressFunc)
	if err != nil {
		return nil, fmt.Errorf("сбор информации о нодах не удался: %v", err)
	}

	updateProgress("Завершено", 100)
	log.Printf("[FIXED] Сканирование с прогрессом завершено, найдено %d нод", len(nodes))
	return nodes, nil
}

// IsTalosNode проверяет, является ли IP адрес нодой Talos
func (s *NetworkScannerImpl) IsTalosNode(ctx context.Context, ip string) bool {
	log.Printf("[FIXED] Проверка, является ли %s нодой Talos", ip)
	log.Printf("[FIXED] Контекст в IsTalosNode: %v", ctx.Err())

	// Проверяем отмену контекста в начале
	select {
	case <-ctx.Done():
		log.Printf("[FIXED] IsTalosNode отменен в начале для IP %s", ip)
		return false
	default:
	}

	// Create context with timeout для этой операции
	timeoutCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	cmd := exec.CommandContext(timeoutCtx, "talosctl", "-n", ip, "get", "machinestatus", "--insecure")
	log.Printf("[FIXED] Создана команда talosctl для IP %s: %v", ip, cmd.Args)

	log.Printf("[FIXED] Выполняем CombinedOutput() для IP %s...", ip)
	output, err := cmd.CombinedOutput()

	// Проверяем причину завершения команды
	if timeoutCtx.Err() != nil {
		if timeoutCtx.Err() == context.DeadlineExceeded {
			log.Printf("[FIXED] Команда talosctl для IP %s превысила таймаут 5 сек", ip)
			return false
		}

		if timeoutCtx.Err() == context.Canceled {
			log.Printf("[FIXED] Команда talosctl для IP %s была отменена", ip)
			return false
		}
	}

	// Проверяем отмену внешнего контекста
	if ctx.Err() != nil {
		log.Printf("[FIXED] Внешний контекст был отменен для IP %s: %v", ip, ctx.Err())
		return false
	}

	if err != nil {
		log.Printf("[FIXED] Команда talosctl не удалась для IP %s: %v, output: %s", ip, err, string(output))
		return false
	}

	log.Printf("[FIXED] IP %s является нодой Talos", ip)
	return true
}

// CollectNodeInfoEnhanced собирает подробную информацию о ноде с использованием NodeManager
func (s *NetworkScannerImpl) CollectNodeInfoEnhanced(ctx context.Context, ip string) (NodeInfo, error) {
	log.Printf("[ENHANCED] Начинаем расширенный сбор информации о ноде для IP: %s", ip)

	node := NodeInfo{
		Name: ip,
		IP:   ip,
	}

	// Получаем имя хоста через NodeManager
	if hostname, err := s.getHostnameViaNodeManager(ctx, ip); err == nil {
		node.Hostname = hostname
	} else {
		node.Hostname = ip
		log.Printf("[ENHANCED] Не удалось получить имя хоста через NodeManager для %s: %v", ip, err)
	}

	// Получаем расширенную информацию об оборудовании
	hardware, err := s.getEnhancedHardwareInfo(ctx, ip)
	if err != nil {
		log.Printf("[ENHANCED] Ошибка получения расширенной информации об оборудовании для %s: %v", ip, err)
		// Возвращаем базовую информацию, даже если не удалось получить детали
		return node, nil
	}
	node.Hardware = hardware

	// Вычисляем ресурсы на основе полученной информации
	node.CPU = 0
	for _, p := range hardware.Processors {
		node.CPU += p.ThreadCount
	}

	node.RAM = hardware.Memory.Size / 1024 // Конвертируем MiB в GiB
	node.Disks = hardware.Blockdevices

	// [COMPATIBILITY] Логи проверки совместимости структур
	log.Printf("[COMPAT-ENHANCED] Заполнение NodeInfo для %s:", ip)
	log.Printf("[COMPAT-ENHANCED] - CPU: %d", node.CPU)
	log.Printf("[COMPAT-ENHANCED] - RAM: %d GiB", node.RAM)
	log.Printf("[COMPAT-ENHANCED] - Disks: %d устройств", len(node.Disks))
	for i, disk := range node.Disks {
		log.Printf("[COMPAT-ENHANCED] - Disk[%d]: Name=%s, DevPath=%s, Size=%d", 
			i, disk.Name, disk.DevPath, disk.Size)
	}

	// Устанавливаем производителя и MAC адрес
	if len(hardware.Processors) > 0 {
		node.Manufacturer = hardware.Processors[0].ProductName
		if hardware.Processors[0].Manufacturer != "" {
			node.Manufacturer = hardware.Processors[0].Manufacturer
		}
	}

	if len(hardware.Interfaces) > 0 {
		node.MAC = hardware.Interfaces[0].MAC
	}

	log.Printf("[ENHANCED] Расширенная информация о ноде %s успешно собрана", ip)
	return node, nil
}

// getHostnameViaNodeManager получает имя хоста через commandExecutor
func (s *NetworkScannerImpl) getHostnameViaNodeManager(ctx context.Context, ip string) (string, error) {
	if s.commandExecutor == nil {
		return "", fmt.Errorf("commandExecutor не инициализирован")
	}

	// Получаем информацию о версии, которая также содержит hostname
	output, err := s.commandExecutor.ExecuteNodeCommand(ctx, ip, "version")
	if err != nil {
		return "", err
	}

	// Парсим hostname из вывода версии
	lines := strings.Split(output, "\n")
	for _, line := range lines {
		if strings.Contains(line, "Hostname:") {
			parts := strings.Split(line, ":")
			if len(parts) >= 2 {
				return strings.TrimSpace(parts[1]), nil
			}
		}
	}

	return ip, nil
}

// getEnhancedHardwareInfo получает расширенную информацию об оборудовании через commandExecutor
func (s *NetworkScannerImpl) getEnhancedHardwareInfo(ctx context.Context, ip string) (Hardware, error) {
	log.Printf("[ENHANCED] Получение расширенной информации об оборудовании для %s", ip)
	var hardware Hardware

	if s.commandExecutor == nil {
		return hardware, fmt.Errorf("commandExecutor не инициализирован")
	}

	// Получаем детальную информацию о памяти
	if memoryInfo, err := s.getMemoryViaNodeManager(ctx, ip); err == nil {
		hardware.Memory = memoryInfo
		log.Printf("[ENHANCED] Получена детальная информация о памяти для %s: %d MiB", ip, memoryInfo.Size)
	} else {
		log.Printf("[ENHANCED] Не удалось получить информацию о памяти через commandExecutor для %s: %v", ip, err)
	}

	// Получаем информацию о дисках (используем прямой метод через talosctl)
	if disksInfo, err := s.getBlockdevices(ctx, ip); err == nil {
		hardware.Blockdevices = disksInfo
		log.Printf("[ENHANCED] Получена информация о %d дисках для %s через getBlockdevices", len(disksInfo), ip)
	} else {
		log.Printf("[ENHANCED] Не удалось получить информацию о дисках через getBlockdevices для %s: %v", ip, err)
		// Попробуем fallback через commandExecutor
		if disksInfo, err := s.getDisksViaNodeManager(ctx, ip); err == nil {
			hardware.Blockdevices = disksInfo
			log.Printf("[ENHANCED] Получена информация о %d дисках через fallback для %s", len(disksInfo), ip)
		} else {
			log.Printf("[ENHANCED] Не удалось получить информацию о дисках через fallback для %s: %v", ip, err)
		}
	}

	// Получаем информацию о процессах (для определения CPU)
	if processesInfo, err := s.getProcessesInfoViaNodeManager(ctx, ip); err == nil {
		// Извлекаем информацию о CPU из процессов
		if len(processesInfo) > 0 {
			// Создаем фиктивный процессор на основе количества процессов
			processor := Processor{
				Manufacturer: "Unknown",
				ProductName:  "CPU (из процессов)",
				ThreadCount:  len(processesInfo),
			}
			hardware.Processors = []Processor{processor}
			log.Printf("[ENHANCED] Получена информация о CPU для %s на основе процессов", ip)
		}
	} else {
		log.Printf("[ENHANCED] Не удалось получить информацию о процессах через commandExecutor для %s: %v", ip, err)
	}

	// Получаем информацию о сетевых интерфейсах
	if interfacesInfo, err := s.getInterfacesViaNodeManager(ctx, ip); err == nil {
		hardware.Interfaces = interfacesInfo
		log.Printf("[ENHANCED] Получена информация о %d сетевых интерфейсах для %s", len(interfacesInfo), ip)
	} else {
		log.Printf("[ENHANCED] Не удалось получить информацию о сетевых интерфейсах через commandExecutor для %s: %v", ip, err)
	}

	log.Printf("[ENHANCED] Расширенная информация об оборудовании для %s успешно получена", ip)
	return hardware, nil
}

// getMemoryViaNodeManager получает информацию о памяти через NodeManager
func (s *NetworkScannerImpl) getMemoryViaNodeManager(ctx context.Context, ip string) (Memory, error) {
	output, err := s.commandExecutor.ExecuteNodeCommand(ctx, ip, "memory")
	if err != nil {
		return Memory{}, err
	}

	// Парсим вывод команды memory
	lines := strings.Split(output, "\n")
	var result Memory
	for _, line := range lines {
		if strings.Contains(line, "Общая память:") {
			parts := strings.Split(line, ":")
			if len(parts) >= 2 {
				sizeStr := strings.TrimSpace(parts[1])
				// Конвертируем в байты (упрощенно)
				if strings.Contains(sizeStr, "GiB") {
					// Извлекаем числовое значение
					var sizeGB int
					if value, err := fmt.Sscanf(sizeStr, "%d GiB", &sizeGB); err == nil && value == 1 {
						result.Size = sizeGB * 1024 // Конвертируем GiB в MiB
						return result, nil
					}
				}
				if strings.Contains(sizeStr, "MiB") {
					if value, err := fmt.Sscanf(sizeStr, "%d MiB", &result.Size); err == nil && value == 1 {
						return result, nil
					}
				}
			}
		}
	}

	return Memory{}, fmt.Errorf("не удалось распарсить информацию о памяти")
}

// getDisksViaNodeManager получает информацию о дисках через NodeManager
func (s *NetworkScannerImpl) getDisksViaNodeManager(ctx context.Context, ip string) ([]Blockdevice, error) {
	log.Printf("[FALLBACK] Получение информации о дисках через NodeManager для %s", ip)
	
	output, err := s.commandExecutor.ExecuteNodeCommand(ctx, ip, "disks")
	if err != nil {
		log.Printf("[FALLBACK] Ошибка выполнения команды disks для %s: %v", ip, err)
		return nil, err
	}

	log.Printf("[FALLBACK] Получен вывод команды disks для %s: %s", ip, output)

	var disks []Blockdevice
	lines := strings.Split(output, "\n")

	for lineNum, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		
		log.Printf("[FALLBACK] Обработка строки %d: %s", lineNum, line)
		
		// Парсим строки вида: "sda\t\t32 GB\tSATA SSD"
		parts := strings.Split(line, "\t")
		if len(parts) >= 2 {
			deviceName := strings.TrimSpace(parts[0])
			sizeStr := strings.TrimSpace(parts[1])
			
			log.Printf("[FALLBACK] Парсинг диска: device=%s, sizeStr=%s", deviceName, sizeStr)
			
			// Конвертируем размер в байты
			var sizeBytes int
			if strings.Contains(sizeStr, "GB") {
				var sizeGB int
				if _, err := fmt.Sscanf(sizeStr, "%d GB", &sizeGB); err == nil {
					sizeBytes = sizeGB * 1024 * 1024 * 1024
					log.Printf("[FALLBACK] Конвертирован размер: %d GB -> %d байт", sizeGB, sizeBytes)
				}
			} else if strings.Contains(sizeStr, "TB") {
				var sizeTB int
				if _, err := fmt.Sscanf(sizeStr, "%d TB", &sizeTB); err == nil {
					sizeBytes = sizeTB * 1024 * 1024 * 1024 * 1024
					log.Printf("[FALLBACK] Конвертирован размер: %d TB -> %d байт", sizeTB, sizeBytes)
				}
			} else {
				log.Printf("[FALLBACK] Неизвестный формат размера: %s", sizeStr)
			}
			
			// Фильтруем слишком маленькие диски (меньше 3GB) и нежелательные устройства
			minSize := 3 * 1024 * 1024 * 1024 // 3GB в байтах
			isUnwantedDevice := strings.HasPrefix(deviceName, "zd") || 
			                    strings.HasPrefix(deviceName, "drbd") ||
			                    strings.HasPrefix(deviceName, "loop") ||
			                    strings.HasPrefix(deviceName, "sr")
			
			if sizeBytes > 0 && sizeBytes >= minSize && !isUnwantedDevice {
				disk := Blockdevice{
					Name:    deviceName,
					Size:    sizeBytes,
					DevPath: "/dev/" + deviceName,
					Metadata: struct {
						ID string `json:"id"`
					}{ID: deviceName},
				}
				
				// Добавляем тип диска если есть
				if len(parts) >= 3 {
					disk.Transport = strings.TrimSpace(parts[2])
				}
				
				// Пытаемся определить модель диска если есть дополнительная информация
				if len(parts) >= 4 {
					disk.Model = strings.TrimSpace(parts[3])
				}
				
				// [COMPATIBILITY] Диагностические логи для fallback метода
				log.Printf("[COMPAT-FALLBACK] Blockdevice создано через fallback для %s:", ip)
				log.Printf("[COMPAT-FALLBACK] - Name: %s (JSON tag: %s)", disk.Name, "исключено из JSON")
				log.Printf("[COMPAT-FALLBACK] - DevPath: %s (JSON tag: %s)", disk.DevPath, "dev_path")
				log.Printf("[COMPAT-FALLBACK] - Size: %d (JSON tag: %s)", disk.Size, "size")
				log.Printf("[COMPAT-FALLBACK] - Model: %s (JSON tag: %s)", disk.Model, "model")
				log.Printf("[COMPAT-FALLBACK] - Transport: %s (JSON tag: %s)", disk.Transport, "transport")
				log.Printf("[COMPAT-FALLBACK] - Metadata.ID: %s (JSON tag: %s)", disk.Metadata.ID, "id")
				
				log.Printf("[FALLBACK] Создан диск: Name=%s, Size=%d, DevPath=%s, Transport=%s, Model=%s", 
					disk.Name, disk.Size, disk.DevPath, disk.Transport, disk.Model)
				
				disks = append(disks, disk)
			} else {
				reasons := []string{}
				if sizeBytes == 0 {
					reasons = append(reasons, "нулевой размер")
				}
				if sizeBytes > 0 && sizeBytes < minSize {
					reasons = append(reasons, fmt.Sprintf("размер %d байт меньше минимального %d байт", sizeBytes, minSize))
				}
				if isUnwantedDevice {
					reasons = append(reasons, "нежелательное устройство")
				}
				
				log.Printf("[FALLBACK] Диск %s пропущен: %v", deviceName, strings.Join(reasons, ", "))
			}
		} else {
			log.Printf("[FALLBACK] Строка %d не содержит достаточно частей: %v", lineNum, parts)
		}
	}

	log.Printf("[FALLBACK] Итого найдено %d дисков через NodeManager для %s", len(disks), ip)
	return disks, nil
}

// getProcessesInfoViaNodeManager получает информацию о процессах через NodeManager
func (s *NetworkScannerImpl) getProcessesInfoViaNodeManager(ctx context.Context, ip string) ([]map[string]interface{}, error) {
	output, err := s.commandExecutor.ExecuteNodeCommand(ctx, ip, "processes")
	if err != nil {
		return nil, err
	}

	var processes []map[string]interface{}
	lines := strings.Split(output, "\n")

	for _, line := range lines {
		if strings.TrimSpace(line) == "" || strings.Contains(line, "PID") || strings.Contains(line, "---") {
			continue
		}

		// Простой парсинг строки процесса
		parts := strings.Fields(line)
		if len(parts) >= 4 {
			process := map[string]interface{}{
				"pid":    parts[0],
				"name":   parts[1],
				"cpu":    parts[2],
				"memory": parts[3],
			}
			processes = append(processes, process)
		}
	}

	return processes, nil
}

// getInterfacesViaNodeManager получает информацию о сетевых интерфейсах через NodeManager
func (s *NetworkScannerImpl) getInterfacesViaNodeManager(ctx context.Context, ip string) ([]Interface, error) {
	// NodeManager не имеет прямой команды для интерфейсов,
	// используем стандартный метод
	return s.getInterfaces(ctx, ip)
}

// CollectNodeInfo собирает подробную информацию о ноде с использованием NodeManager
func (s *NetworkScannerImpl) CollectNodeInfo(ctx context.Context, ip string) (NodeInfo, error) {
	return s.CollectNodeInfoEnhanced(ctx, ip)
}

// ParallelScan параллельно сканирует список IP адресов
func (s *NetworkScannerImpl) ParallelScan(ctx context.Context, ips []string) ([]NodeInfo, error) {
	return s.parallelScanWithProgress(ctx, ips, nil)
}

// parallelScanWithProgress внутренняя функция для параллельного сканирования с прогрессом
func (s *NetworkScannerImpl) parallelScanWithProgress(ctx context.Context, ips []string, progressFunc func(int)) ([]NodeInfo, error) {
	total := len(ips)
	if total == 0 {
		log.Printf("[FIXED] Нет IP адресов для сканирования")
		return nil, nil
	}

	log.Printf("[FIXED] Запуск параллельного сканирования %d IP адресов", total)
	log.Printf("[FIXED] progressFunc == nil: %v", progressFunc == nil)

	// Настройки worker pool
	numWorkers := 10
	if total < numWorkers {
		numWorkers = total
	}

	log.Printf("[DIAGNOSTIC] Настройка пула воркеров: %d воркеров для %d IP адресов", numWorkers, total)

	type job struct {
		index int
		ip    string
	}

	type result struct {
		index int
		node  NodeInfo
		err   error
		found bool
	}

	jobChan := make(chan job, total)
	resultChan := make(chan result, total)

	// Флаг для early termination
	foundNodes := make([]NodeInfo, 0, total)
	var foundMutex sync.Mutex
	targetNodes := 3 // Максимум нод для поиска (early termination)

	// Запускаем воркеры
	var wg sync.WaitGroup
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	for w := 0; w < numWorkers; w++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			log.Printf("[FIXED] Воркер %d запущен", workerID)

			for j := range jobChan {
				// Проверяем early termination
				foundMutex.Lock()
				shouldStop := len(foundNodes) >= targetNodes
				foundMutex.Unlock()

				if shouldStop {
					log.Printf("[FIXED] Воркер %d остановлен: достигнуто целевое количество нод (%d)", workerID, targetNodes)
					return
				}

				select {
				case <-ctx.Done():
					log.Printf("[FIXED] Воркер %d отменен из-за контекста", workerID)
					return
				default:
				}

				var res result
				res.index = j.index

				// Проверяем, является ли IP нодой Talos
				if s.IsTalosNode(ctx, j.ip) {
					log.Printf("[FIXED] Воркер %d: IP %s является нодой Talos, собираем информацию", workerID, j.ip)
					node, err := s.CollectNodeInfo(ctx, j.ip)
					if err == nil {
						res.node = node
						res.found = true

						// Добавляем в найденные ноды для early termination
						foundMutex.Lock()
						foundNodes = append(foundNodes, node)
						log.Printf("[FIXED] Воркер %d: найдена нода %s, всего найдено: %d", workerID, node.Hostname, len(foundNodes))
						foundMutex.Unlock()
					} else {
						res.err = err
						log.Printf("[FIXED] Воркер %d: ошибка сбора информации о %s: %v", workerID, j.ip, err)
					}
				} else {
					log.Printf("[FIXED] Воркер %d: IP %s не является нодой Talos", workerID, j.ip)
				}

				log.Printf("[FIXED] Воркер %d отправляет результат для IP %s (найдено: %v)", workerID, j.ip, res.found)
				resultChan <- res
			}
			log.Printf("[FIXED] Воркер %d завершен", workerID)
		}(w)
	}

	// Отправляем задачи
	go func() {
		log.Printf("Начинаем отправку задач в jobChan")
		for i, ip := range ips {
			select {
			case <-ctx.Done():
				log.Printf("Контекст отменен во время отправки задач")
				return
			default:
				log.Printf("Отправляем задачу для IP %s (индекс %d)", ip, i)
				jobChan <- job{index: i, ip: ip}
			}
		}
		log.Printf("Закрываем jobChan")
		close(jobChan)
	}()

	// Ждем завершения воркеров
	go func() {
		log.Printf("Ждем завершения воркеров")
		wg.Wait()
		log.Printf("Все воркеры завершены, закрываем resultChan")
		close(resultChan)
	}()

	// Собираем результаты
	results := make([]result, total)
	completed := 0
	foundCount := 0
	log.Printf("[FIXED] Начинаем сбор результатов из resultChan")

	for res := range resultChan {
		log.Printf("Получен результат для индекса %d, найдено: %v", res.index, res.found)
		results[res.index] = res
		completed++

		if res.found {
			foundCount++
		}

		// Обновляем прогресс
		if progressFunc != nil {
			newProgress := 20 + completed*80/total // 20% уже пройдено, осталось 80%
			if newProgress > 100 {
				newProgress = 100
			}
			log.Printf("[FIXED] Вызов progressFunc с %d%% (completed: %d, total: %d)", newProgress, completed, total)
			progressFunc(newProgress)
			log.Printf("[FIXED] Прогресс обновлен: %d%% (%d/%d задач, %d нод найдено)", newProgress, completed, total, foundCount)
		} else {
			log.Printf("[FIXED] progressFunc == nil в parallelScanWithProgress!")
		}
	}

	log.Printf("Сбор результатов завершен, всего завершено: %d", completed)

	// Фильтруем найденные ноды
	var filteredNodes []NodeInfo
	for _, res := range results {
		if res.found && res.err == nil && res.node.IP != "" {
			filteredNodes = append(filteredNodes, res.node)
		}
	}

	return filteredNodes, nil
}

// scanForTalOSNodes сканирует сеть на наличие нод с открытым портом 50000
func (s *NetworkScannerImpl) scanForTalOSNodes(ctx context.Context, cidr string) ([]string, error) {
	log.Printf("[FIXED] Сканирование сети %s на наличие нод с открытым портом 50000", cidr)
	log.Printf("[FIXED] Контекст сканирования: %v", ctx.Err())

	// Проверяем отмену контекста в начале
	select {
	case <-ctx.Done():
		log.Printf("[FIXED] Сканирование отменено в начале scanForTalOSNodes")
		return nil, fmt.Errorf("сканирование отменено: %v", ctx.Err())
	default:
	}

	var output []byte
	var err error

	// Первая попытка с таймаутом
	{
		log.Printf("[FIXED] Выполняем первую команду nmap...")
		cmd := exec.Command("nmap", "-p", "50000", "--open", "-oG", "-", cidr)

		// Проверяем отмену перед выполнением команды
		select {
		case <-ctx.Done():
			log.Printf("[FIXED] Сканирование отменено перед первой командой nmap")
			return nil, fmt.Errorf("сканирование отменено: %v", ctx.Err())
		default:
		}

		cmd = exec.CommandContext(ctx, cmd.Args[0], cmd.Args[1:]...)

		output, err = cmd.Output()

		// Проверяем отмену после выполнения команды
		if ctx.Err() != nil {
			if ctx.Err() == context.DeadlineExceeded {
				log.Printf("[FIXED] Первая команда nmap превысила таймаут или была отменена")
				err = fmt.Errorf("nmap timeout or cancelled after 15 seconds")
			} else {
				log.Printf("[FIXED] Первая команда nmap была отменена: %v", ctx.Err())
				return nil, fmt.Errorf("сканирование отменено: %v", ctx.Err())
			}
		}
	}

	if err != nil {
		log.Printf("[FIXED] Первая команда nmap не удалась: %v, пробуем альтернативу", err)

		// Проверяем отмену перед второй попыткой
		select {
		case <-ctx.Done():
			log.Printf("[FIXED] Сканирование отменено перед второй командой nmap")
			return nil, fmt.Errorf("сканирование отменено: %v", ctx.Err())
		default:
		}

		// Вторая попытка с альтернативными параметрами
		{
			log.Printf("[FIXED] Выполняем альтернативную команду nmap...")
			cmd := exec.Command("nmap", "-p", "50000", "-sT", "--open", "-oG", "-", cidr)

			cmd = exec.CommandContext(ctx, cmd.Args[0], cmd.Args[1:]...)

			output, err = cmd.Output()

			// Проверяем отмену после второй команды
			if ctx.Err() != nil {
				if ctx.Err() == context.DeadlineExceeded {
					log.Printf("[FIXED] Альтернативная команда nmap превысила таймаут или была отменена")
					err = fmt.Errorf("alternative nmap timeout or cancelled after 10 seconds")
				} else {
					log.Printf("[FIXED] Альтернативная команда nmap была отменена: %v", ctx.Err())
					return nil, fmt.Errorf("сканирование отменено: %v", ctx.Err())
				}
			}
		}

		if err != nil {
			log.Printf("[FIXED] Альтернативная команда nmap не удалась: %v", err)
			return nil, fmt.Errorf("nmap scan failed: %v", err)
		}
	}

	// Проверяем отмену перед обработкой результатов
	select {
	case <-ctx.Done():
		log.Printf("[FIXED] Сканирование отменено при обработке результатов")
		return nil, fmt.Errorf("сканирование отменено: %v", ctx.Err())
	default:
	}

	log.Printf("[FIXED] Команда nmap выполнена успешно, длина вывода: %d", len(output))

	outputStr := string(output)
	var ips []string
	lines := strings.Split(outputStr, "\n")

	for _, line := range lines {
		line = strings.TrimSpace(line)

		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		// Проверяем отмену в цикле обработки строк
		select {
		case <-ctx.Done():
			log.Printf("[FIXED] Сканирование отменено при обработке строк результатов")
			return nil, fmt.Errorf("сканирование отменено: %v", ctx.Err())
		default:
		}

		// Проверяем, содержит ли строка информацию об открытом порте 50000
		if strings.Contains(line, "50000/open") {
			// Ищем IP в строке
			re := regexp.MustCompile(`\b(?:\d{1,3}\.){3}\d{1,3}\b`)
			matches := re.FindAllString(line, -1)

			for _, match := range matches {
				if parsedIP := net.ParseIP(match); parsedIP != nil {
					// Проверяем, не дубликат ли это
					duplicate := false
					for _, existing := range ips {
						if existing == match {
							duplicate = true
							break
						}
					}

					if !duplicate {
						ips = append(ips, match)
					}
				}
			}
		}
	}

	log.Printf("Найдено IP адресов с открытым портом 50000: %v", ips)
	return ips, nil
}

// getHostname получает имя хоста для IP адреса
func (s *NetworkScannerImpl) getHostname(ctx context.Context, ip string) (string, error) {
	// Проверяем отмену контекста в начале
	select {
	case <-ctx.Done():
		return "", fmt.Errorf("getHostname отменен для %s: %v", ip, ctx.Err())
	default:
	}

	// Create context with timeout для этой операции
	timeoutCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()

	cmd := exec.CommandContext(timeoutCtx, "talosctl", "-e", ip, "-n", ip, "get", "hostname", "-i", "-o", "jsonpath={.spec.hostname}")
	log.Printf("[FIXED] Получение hostname для %s: %v", ip, cmd.Args)

	output, err := cmd.Output()
	if timeoutCtx.Err() != nil {
		if timeoutCtx.Err() == context.DeadlineExceeded {
			log.Printf("[FIXED] Timeout при получении hostname для %s", ip)
			return "", fmt.Errorf("hostname timeout for %s", ip)
		}
		if timeoutCtx.Err() == context.Canceled {
			log.Printf("[FIXED] Команда hostname отменена для %s", ip)
			return "", fmt.Errorf("hostname cancelled for %s", ip)
		}
	}

	// Проверяем отмену внешнего контекста
	if ctx.Err() != nil {
		log.Printf("[FIXED] Внешний контекст отменен при получении hostname для %s: %v", ip, ctx.Err())
		return "", fmt.Errorf("getHostname отменен для %s: %v", ip, ctx.Err())
	}

	if err != nil {
		log.Printf("[FIXED] Ошибка при получении hostname для %s: %v", ip, err)
		return "", err
	}

	result := strings.TrimSpace(string(output))
	log.Printf("[FIXED] Hostname для %s: %s", ip, result)
	return result, nil
}

// getHardwareInfo получает информацию об оборудовании ноды
func (s *NetworkScannerImpl) getHardwareInfo(ctx context.Context, ip string) (Hardware, error) {
	log.Printf("Получение информации об оборудовании для %s", ip)
	var hardware Hardware

	// Получаем процессоры
	log.Printf("Получение процессоров для %s", ip)
	processors, err := s.getProcessors(ctx, ip)
	if err != nil {
		return hardware, fmt.Errorf("не удалось получить процессоры: %v", err)
	}
	log.Printf("Получено %d процессоров для %s", len(processors), ip)
	hardware.Processors = processors

	// Получаем память
	log.Printf("Получение памяти для %s", ip)
	memory, err := s.getMemory(ctx, ip)
	if err != nil {
		return hardware, fmt.Errorf("не удалось получить память: %v", err)
	}
	log.Printf("Получена память для %s: %d MiB", ip, memory.Size)
	hardware.Memory = memory

	// Получаем блочные устройства
	log.Printf("Получение блочных устройств для %s", ip)
	blockdevices, err := s.getBlockdevices(ctx, ip)
	if err != nil {
		log.Printf("Не удалось получить блочные устройства для %s: %v, продолжаем без них", ip, err)
		blockdevices = []Blockdevice{}
	}
	log.Printf("Получено %d блочных устройств для %s", len(blockdevices), ip)
	hardware.Blockdevices = blockdevices

	// Получаем сетевые интерфейсы
	log.Printf("Получение сетевых интерфейсов для %s", ip)
	interfaces, err := s.getInterfaces(ctx, ip)
	if err != nil {
		log.Printf("Не удалось получить сетевые интерфейсы для %s: %v, продолжаем без них", ip, err)
		interfaces = []Interface{}
	}
	log.Printf("Получено %d сетевых интерфейсов для %s", len(interfaces), ip)
	hardware.Interfaces = interfaces

	log.Printf("Информация об оборудовании для %s успешно получена", ip)
	return hardware, nil
}

// getProcessors получает информацию о процессорах
func (s *NetworkScannerImpl) getProcessors(ctx context.Context, ip string) ([]Processor, error) {
	// Проверяем отмену контекста в начале
	select {
	case <-ctx.Done():
		return nil, fmt.Errorf("getProcessors отменен для %s: %v", ip, ctx.Err())
	default:
	}

	// Create context with timeout для этой операции
	timeoutCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()

	cmd := exec.CommandContext(timeoutCtx, "talosctl", "-e", ip, "-n", ip, "get", "cpu", "-i", "-o", "json")
	log.Printf("[FIXED] Получение CPU для %s: %v", ip, cmd.Args)

	output, err := cmd.Output()
	if timeoutCtx.Err() != nil {
		if timeoutCtx.Err() == context.DeadlineExceeded {
			log.Printf("[FIXED] Timeout при получении CPU для %s", ip)
			return nil, fmt.Errorf("cpu timeout for %s", ip)
		}
		if timeoutCtx.Err() == context.Canceled {
			log.Printf("[FIXED] Команда CPU отменена для %s", ip)
			return nil, fmt.Errorf("cpu cancelled for %s", ip)
		}
	}

	// Проверяем отмену внешнего контекста
	if ctx.Err() != nil {
		log.Printf("[FIXED] Внешний контекст отменен при получении CPU для %s: %v", ip, ctx.Err())
		return nil, fmt.Errorf("getProcessors отменен для %s: %v", ip, ctx.Err())
	}

	if err != nil {
		log.Printf("[FIXED] Ошибка при получении CPU для %s: %v", ip, err)
		return nil, err
	}

	var resp struct {
		Spec Processor `json:"spec"`
	}
	if err := json.Unmarshal(output, &resp); err != nil {
		log.Printf("[FIXED] Ошибка декодирования CPU для %s: %v", ip, err)
		return nil, err
	}

	log.Printf("[FIXED] Получен CPU для %s: %v", ip, resp.Spec)
	return []Processor{resp.Spec}, nil
}

// getMemory получает информацию о памяти
func (s *NetworkScannerImpl) getMemory(ctx context.Context, ip string) (Memory, error) {
	// Проверяем отмену контекста в начале
	select {
	case <-ctx.Done():
		return Memory{}, fmt.Errorf("getMemory отменен для %s: %v", ip, ctx.Err())
	default:
	}

	// Create context with timeout для этой операции
	timeoutCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()

	cmd := exec.CommandContext(timeoutCtx, "talosctl", "-e", ip, "-n", ip, "get", "ram", "-i", "-o", "json")
	log.Printf("[FIXED] Получение RAM для %s: %v", ip, cmd.Args)

	output, err := cmd.Output()
	if timeoutCtx.Err() != nil {
		if timeoutCtx.Err() == context.DeadlineExceeded {
			log.Printf("[FIXED] Timeout при получении RAM для %s", ip)
			return Memory{}, fmt.Errorf("ram timeout for %s", ip)
		}
		if timeoutCtx.Err() == context.Canceled {
			log.Printf("[FIXED] Команда RAM отменена для %s", ip)
			return Memory{}, fmt.Errorf("ram cancelled for %s", ip)
		}
	}

	// Проверяем отмену внешнего контекста
	if ctx.Err() != nil {
		log.Printf("[FIXED] Внешний контекст отменен при получении RAM для %s: %v", ip, ctx.Err())
		return Memory{}, fmt.Errorf("getMemory отменен для %s: %v", ip, ctx.Err())
	}

	if err != nil {
		log.Printf("[FIXED] Ошибка при получении RAM для %s: %v", ip, err)
		return Memory{}, err
	}

	var resp struct {
		Spec Memory `json:"spec"`
	}
	if err := json.Unmarshal(output, &resp); err != nil {
		log.Printf("[FIXED] Ошибка декодирования RAM для %s: %v", ip, err)
		return Memory{}, err
	}

	log.Printf("[FIXED] Получена RAM для %s: %d MiB", ip, resp.Spec.Size)
	return resp.Spec, nil
}

// getBlockdevices получает информацию о блочных устройствах
func (s *NetworkScannerImpl) getBlockdevices(ctx context.Context, ip string) ([]Blockdevice, error) {
	// Проверяем отмену контекста в начале
	select {
	case <-ctx.Done():
		return nil, fmt.Errorf("getBlockdevices отменен для %s: %v", ip, ctx.Err())
	default:
	}

	// Create context with timeout для этой операции
	timeoutCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	cmd := exec.CommandContext(timeoutCtx, "talosctl", "-e", ip, "-n", ip, "get", "disks", "-i", "-o", "json")
	log.Printf("[FIXED] Получение дисков для %s: %v", ip, cmd.Args)

	output, err := cmd.Output()
	if timeoutCtx.Err() != nil {
		if timeoutCtx.Err() == context.DeadlineExceeded {
			log.Printf("[FIXED] Timeout при получении дисков для %s", ip)
			return nil, fmt.Errorf("disks timeout for %s", ip)
		}
		if timeoutCtx.Err() == context.Canceled {
			log.Printf("[FIXED] Команда дисков отменена для %s", ip)
			return nil, fmt.Errorf("disks cancelled for %s", ip)
		}
	}

	// Проверяем отмену внешнего контекста
	if ctx.Err() != nil {
		log.Printf("[FIXED] Внешний контекст отменен при получении дисков для %s: %v", ip, ctx.Err())
		return nil, fmt.Errorf("getBlockdevices отменен для %s: %v", ip, ctx.Err())
	}

	if err != nil {
		log.Printf("[FIXED] Ошибка при получении дисков для %s: %v", ip, err)
		return nil, err
	}

	decoder := json.NewDecoder(strings.NewReader(string(output)))
	var blockdevices []Blockdevice
	for {
		// Проверяем отмену в цикле декодирования
		select {
		case <-ctx.Done():
			log.Printf("[FIXED] Отменен при декодировании дисков для %s", ip)
			return nil, fmt.Errorf("getBlockdevices отменен при декодировании для %s: %v", ip, ctx.Err())
		default:
		}

		var resp struct {
			Metadata struct {
				ID string `json:"id"`
			} `json:"metadata"`
			Spec struct {
				Size      int    `json:"size"`
				DevPath   string `json:"dev_path"`
				Model     string `json:"model"`
				Transport string `json:"transport"`
			} `json:"spec"`
		}
		if err := decoder.Decode(&resp); err != nil {
			if err == io.EOF {
				break
			}
			log.Printf("[FIXED] Не удалось декодировать JSON объект для %s: %v", ip, err)
			return nil, err
		}
		bd := Blockdevice{
			Name:      resp.Metadata.ID,
			Size:      resp.Spec.Size,
			DevPath:   resp.Spec.DevPath,
			Model:     resp.Spec.Model,
			Transport: resp.Spec.Transport,
			Metadata:  resp.Metadata,
		}
		
		// [COMPATIBILITY] Диагностические логи для проверки совместимости структур
		log.Printf("[COMPAT] Blockdevice создано для %s:", ip)
		log.Printf("[COMPAT] - Name: %s (JSON tag: %s)", bd.Name, "исключено из JSON")
		log.Printf("[COMPAT] - DevPath: %s (JSON tag: %s)", bd.DevPath, "dev_path")
		log.Printf("[COMPAT] - Size: %d (JSON tag: %s)", bd.Size, "size")
		log.Printf("[COMPAT] - Model: %s (JSON tag: %s)", bd.Model, "model")
		log.Printf("[COMPAT] - Transport: %s (JSON tag: %s)", bd.Transport, "transport")
		log.Printf("[COMPAT] - Metadata.ID: %s (JSON tag: %s)", bd.Metadata.ID, "id")
		blockdevices = append(blockdevices, bd)
	}

	// Фильтруем нежелательные устройства и маленькие диски
	var filtered []Blockdevice
	minSize := 3 * 1024 * 1024 * 1024 // 3 GB
	for _, bd := range blockdevices {
		if !strings.HasPrefix(bd.Name, "zd") && !strings.HasPrefix(bd.Name, "drbd") &&
			!strings.HasPrefix(bd.Name, "loop") && !strings.HasPrefix(bd.Name, "sr") && bd.Size >= minSize {
			filtered = append(filtered, bd)
		}
	}

	log.Printf("[FIXED] Получено %d дисков для %s", len(filtered), ip)
	return filtered, nil
}

// getInterfaceIPs получает IP адреса интерфейса через talosctl
func (s *NetworkScannerImpl) getInterfaceIPs(ctx context.Context, ip, interfaceName string) ([]string, error) {
	// Проверяем отмену контекста в начале
	select {
	case <-ctx.Done():
		return nil, fmt.Errorf("getInterfaceIPs отменен для %s: %v", ip, ctx.Err())
	default:
	}

	// Create context with timeout для этой операции
	timeoutCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	cmd := exec.CommandContext(timeoutCtx, "talosctl", "-e", ip, "-n", ip, "get", "addresses", "-i", "-o", "json")
	log.Printf("[FIXED] Получение IP адресов для интерфейса %s на %s: %v", interfaceName, ip, cmd.Args)

	output, err := cmd.Output()
	if timeoutCtx.Err() != nil {
		if timeoutCtx.Err() == context.DeadlineExceeded {
			log.Printf("[FIXED] Timeout при получении IP адресов для интерфейса %s на %s", interfaceName, ip)
			return nil, fmt.Errorf("interface IPs timeout for %s:%s", ip, interfaceName)
		}
		if timeoutCtx.Err() == context.Canceled {
			log.Printf("[FIXED] Команда IP адресов отменена для интерфейса %s на %s", interfaceName, ip)
			return nil, fmt.Errorf("interface IPs cancelled for %s:%s", ip, interfaceName)
		}
	}

	// Проверяем отмену внешнего контекста
	if ctx.Err() != nil {
		log.Printf("[FIXED] Внешний контекст отменен при получении IP адресов для %s:%s: %v", ip, interfaceName, ctx.Err())
		return nil, fmt.Errorf("getInterfaceIPs отменен для %s:%s: %v", ip, interfaceName, ctx.Err())
	}

	if err != nil {
		log.Printf("[FIXED] Ошибка при получении IP адресов для интерфейса %s на %s: %v", interfaceName, ip, err)
		return nil, err
	}

	var ips []string
	decoder := json.NewDecoder(strings.NewReader(string(output)))
	for {
		// Проверяем отмену в цикле декодирования
		select {
		case <-ctx.Done():
			log.Printf("[FIXED] Отменен при декодировании IP адресов для %s:%s", ip, interfaceName)
			return nil, fmt.Errorf("getInterfaceIPs отменен при декодировании для %s:%s: %v", ip, interfaceName, ctx.Err())
		default:
		}

		var resp struct {
			Spec struct {
				Address   string `json:"address"`
				LinkName  string `json:"linkName"`
			} `json:"spec"`
		}
		if err := decoder.Decode(&resp); err != nil {
			if err == io.EOF {
				break
			}
			log.Printf("[FIXED] Ошибка декодирования IP адресов для %s:%s: %v", ip, interfaceName, err)
			return nil, err
		}

		// Фильтруем только адреса для нужного интерфейса
		if resp.Spec.LinkName == interfaceName {
			ips = append(ips, resp.Spec.Address)
		}
	}

	log.Printf("[FIXED] Получено %d IP адресов для интерфейса %s на %s: %v", len(ips), interfaceName, ip, ips)
	return ips, nil
}

// getInterfaceStatus получает статус интерфейса через talosctl
func (s *NetworkScannerImpl) getInterfaceStatus(ctx context.Context, ip, interfaceName string) (string, bool, error) {
	// Проверяем отмену контекста в начале
	select {
	case <-ctx.Done():
		return "", false, fmt.Errorf("getInterfaceStatus отменен для %s: %v", ip, ctx.Err())
	default:
	}

	// Create context with timeout для этой операции
	timeoutCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()

	cmd := exec.CommandContext(timeoutCtx, "talosctl", "-e", ip, "-n", ip, "get", "link", "-i", "-o", "json")
	log.Printf("[FIXED] Получение статуса интерфейса %s на %s: %v", interfaceName, ip, cmd.Args)

	output, err := cmd.Output()
	if timeoutCtx.Err() != nil {
		if timeoutCtx.Err() == context.DeadlineExceeded {
			log.Printf("[FIXED] Timeout при получении статуса интерфейса %s на %s", interfaceName, ip)
			return "", false, fmt.Errorf("interface status timeout for %s:%s", ip, interfaceName)
		}
		if timeoutCtx.Err() == context.Canceled {
			log.Printf("[FIXED] Команда статуса отменена для интерфейса %s на %s", interfaceName, ip)
			return "", false, fmt.Errorf("interface status cancelled for %s:%s", ip, interfaceName)
		}
	}

	// Проверяем отмену внешнего контекста
	if ctx.Err() != nil {
		log.Printf("[FIXED] Внешний контекст отменен при получении статуса для %s:%s: %v", ip, interfaceName, ctx.Err())
		return "", false, fmt.Errorf("getInterfaceStatus отменен для %s:%s: %v", ip, interfaceName, ctx.Err())
	}

	if err != nil {
		log.Printf("[FIXED] Ошибка при получении статуса интерфейса %s на %s: %v", interfaceName, ip, err)
		return "", false, err
	}

	decoder := json.NewDecoder(strings.NewReader(string(output)))
	for {
		// Проверяем отмену в цикле декодирования
		select {
		case <-ctx.Done():
			log.Printf("[FIXED] Отменен при декодировании статуса для %s:%s", ip, interfaceName)
			return "", false, fmt.Errorf("getInterfaceStatus отменен при декодировании для %s:%s: %v", ip, interfaceName, ctx.Err())
		default:
		}

		var resp struct {
			Spec struct {
				Name             string `json:"name"`
				Logical          bool   `json:"logical"`
				Up               bool   `json:"up"`
				BcastValid       bool   `json:"bcast"`
				MTU              int    `json:"mtu"`
				Kind             string `json:"kind"`
				HardwareAddr     string `json:"hardwareAddr"`
				LinkState        bool   `json:"linkState"`
				OperationalState string `json:"operationalState"`
			} `json:"spec"`
		}
		if err := decoder.Decode(&resp); err != nil {
			if err == io.EOF {
				break
			}
			log.Printf("[FIXED] Ошибка декодирования статуса для %s:%s: %v", ip, interfaceName, err)
			return "", false, err
		}

		// Находим нужный интерфейс
		if resp.Spec.Name == interfaceName {
			status := "down"
			if resp.Spec.Up {
				status = "up"
			}
			log.Printf("[FIXED] Получен статус %s для интерфейса %s на %s (up=%v)", status, interfaceName, ip, resp.Spec.Up)
			return status, resp.Spec.Up, nil
		}
	}

	log.Printf("[FIXED] Интерфейс %s не найден при получении статуса на %s", interfaceName, ip)
	return "unknown", false, fmt.Errorf("interface %s not found", interfaceName)
}

// getInterfaces получает информацию о сетевых интерфейсах
func (s *NetworkScannerImpl) getInterfaces(ctx context.Context, ip string) ([]Interface, error) {
	// Проверяем отмену контекста в начале
	select {
	case <-ctx.Done():
		return nil, fmt.Errorf("getInterfaces отменен для %s: %v", ip, ctx.Err())
	default:
	}

	// Create context with timeout для этой операции
	timeoutCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	log.Printf("[INTERFACES] Получение сетевых интерфейсов для %s", ip)

	// Получаем информацию о линках и адресах одновременно
	// Сначала линки
	cmd := exec.CommandContext(timeoutCtx, "talosctl", "-e", ip, "-n", ip, "get", "link", "-i", "-o", "json")
	log.Printf("[INTERFACES] Получение линков для %s: %v", ip, cmd.Args)

	output, err := cmd.Output()
	if timeoutCtx.Err() != nil {
		if timeoutCtx.Err() == context.DeadlineExceeded {
			log.Printf("[INTERFACES] Timeout при получении линков для %s", ip)
			return nil, fmt.Errorf("interfaces timeout for %s", ip)
		}
		if timeoutCtx.Err() == context.Canceled {
			log.Printf("[INTERFACES] Команда линков отменена для %s", ip)
			return nil, fmt.Errorf("interfaces cancelled for %s", ip)
		}
	}

	// Проверяем отмену внешнего контекста
	if ctx.Err() != nil {
		log.Printf("[INTERFACES] Внешний контекст отменен при получении линков для %s: %v", ip, ctx.Err())
		return nil, fmt.Errorf("getInterfaces отменен для %s: %v", ip, ctx.Err())
	}

	if err != nil {
		log.Printf("[INTERFACES] Ошибка при получении линков для %s: %v", ip, err)
		return nil, err
	}

	log.Printf("[INTERFACES] Raw links output: %s", string(output))

	// Парсим линки
	decoder := json.NewDecoder(strings.NewReader(string(output)))
	var rawInterfaces []struct {
		Metadata struct {
			ID string `json:"id"`
		} `json:"metadata"`
		Spec struct {
			Name         string `json:"name"`
			HardwareAddr string `json:"hardwareAddr"`
			LinkState    bool   `json:"linkState"`
			Up           bool   `json:"up"`
			Kind         string `json:"kind"`
			MTU          int    `json:"mtu"`
		} `json:"spec"`
	}
	
	for {
		var resp struct {
			Metadata struct {
				ID string `json:"id"`
			} `json:"metadata"`
			Spec struct {
				Name         string `json:"name"`
				HardwareAddr string `json:"hardwareAddr"`
				LinkState    bool   `json:"linkState"`
				Up           bool   `json:"up"`
				Kind         string `json:"kind"`
				MTU          int    `json:"mtu"`
			} `json:"spec"`
		}
		if err := decoder.Decode(&resp); err != nil {
			if err == io.EOF {
				break
			}
			log.Printf("[INTERFACES] Ошибка декодирования линков для %s: %v", ip, err)
			return nil, err
		}
		rawInterfaces = append(rawInterfaces, resp)
	}

	// Получаем IP адреса
	cmd = exec.CommandContext(timeoutCtx, "talosctl", "-e", ip, "-n", ip, "get", "addresses", "-i", "-o", "json")
	log.Printf("[INTERFACES] Получение адресов для %s: %v", ip, cmd.Args)
	
	output, err = cmd.Output()
	if err != nil {
		log.Printf("[INTERFACES] Ошибка получения адресов для %s: %v", ip, err)
		// Продолжаем без IP адресов
	}

	// Парсим IP адреса
	var allIPs []string
	if err == nil {
		decoder := json.NewDecoder(strings.NewReader(string(output)))
		for {
			var resp struct {
				Spec struct {
					Address  string `json:"address"`
					LinkName string `json:"linkName"`
				} `json:"spec"`
			}
			if err := decoder.Decode(&resp); err != nil {
				if err == io.EOF {
					break
				}
				log.Printf("[INTERFACES] Ошибка декодирования адресов для %s: %v", ip, err)
				break
			}
			allIPs = append(allIPs, fmt.Sprintf("%s %s", resp.Spec.LinkName, resp.Spec.Address))
		}
	}

	log.Printf("[INTERFACES] Получено %d линков и %d IP адресов для %s", len(rawInterfaces), len(allIPs), ip)

	// Фильтруем и формируем финальный список интерфейсов согласно shell-скрипту
	var interfaces []Interface
	for _, rawIface := range rawInterfaces {
		// Используем metadata.id как имя интерфейса (как в shell-скрипте)
		interfaceName := rawIface.Metadata.ID
		if interfaceName == "" {
			interfaceName = rawIface.Spec.Name // fallback на spec.name
		}
		
		// Фильтрация согласно shell-скрипту: /^(ID|eno|eth|enp|enx|ens|bond)/
		if interfaceName == "lo" || interfaceName == "docker0" || strings.HasPrefix(interfaceName, "br-") || 
			strings.HasPrefix(interfaceName, "veth") || strings.HasPrefix(interfaceName, "cali") {
			log.Printf("[INTERFACES] Пропускаем нежелательный интерфейс: %s", interfaceName)
			continue
		}
		
		// Проверяем соответствие паттерну валидных имен
		matched := false
		validPrefixes := []string{"eno", "eth", "enp", "enx", "ens", "bond"}
		for _, prefix := range validPrefixes {
			if strings.HasPrefix(interfaceName, prefix) {
				matched = true
				break
			}
		}
		
		// Если не matched по префиксу, но есть MAC адрес - включаем (для виртуальных интерфейсов)
		if !matched && rawIface.Spec.HardwareAddr == "" {
			log.Printf("[INTERFACES] Пропускаем интерфейс без MAC и без валидного префикса: %s", interfaceName)
			continue
		}
		
		// Находим IP адреса для этого интерфейса
		var interfaceIPs []string
		for _, ipEntry := range allIPs {
			parts := strings.Fields(ipEntry)
			if len(parts) >= 2 && parts[0] == interfaceName {
				interfaceIPs = append(interfaceIPs, parts[1])
			}
		}
		
		// Создаем структуру интерфейса
		iface := Interface{
			Name: interfaceName,
			MAC:  rawIface.Spec.HardwareAddr,
			IPs:  interfaceIPs,
		}
		
		interfaces = append(interfaces, iface)
		
		log.Printf("[INTERFACES] Добавлен интерфейс: %s [MAC: %s] [IPs: %v]", 
				interfaceName, rawIface.Spec.HardwareAddr, interfaceIPs)
	}

	log.Printf("[INTERFACES] Финальный список: %d сетевых интерфейсов для %s", len(interfaces), ip)
	return interfaces, nil
}
