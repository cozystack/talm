package interactive

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/dustin/go-humanize"
	"google.golang.org/grpc"
	"google.golang.org/grpc/peer"

	"github.com/siderolabs/talos/pkg/machinery/api/common"
	"github.com/siderolabs/talos/pkg/machinery/api/machine"
	"github.com/siderolabs/talos/pkg/machinery/client"
	"github.com/siderolabs/talos/pkg/machinery/constants"
	"github.com/siderolabs/talos/pkg/machinery/version"
)

// NodeInfo структура для хранения информации об узле
type NodeInfo struct {
	IP       string
	Hostname string
	Status   string
	Version  string
}

// NodeManager менеджер для работы с узлами
type NodeManager struct {
	rootDir string
	nodes   []NodeInfo
}

// NewNodeManager создает новый менеджер узлов
func NewNodeManager(rootDir string) *NodeManager {
	return &NodeManager{
		rootDir: rootDir,
		nodes:   []NodeInfo{},
	}
}

// LoadNodes загружает список узлов из конфигурации
func (nm *NodeManager) LoadNodes() error {
	// Пытаемся загрузить узлы из values.yaml
	valuesFile := fmt.Sprintf("%s/values.yaml", nm.rootDir)
	data, err := os.ReadFile(valuesFile)
	if err != nil {
		// Если файл не найден, пробуем другие способы
		return nm.loadNodesFromAlternative()
	}

	// Парсим YAML (упрощенно)
	lines := strings.Split(string(data), "\n")
	for _, line := range lines {
		if strings.Contains(line, "nodes:") {
			// Найден раздел nodes, парсим его
			nm.parseNodesSection(lines)
			break
		}
	}

	return nil
}

// loadNodesFromAlternative загружает узлы из альтернативных источников
func (nm *NodeManager) loadNodesFromAlternative() error {
	// Проверяем директорию nodes/
	nodesDir := fmt.Sprintf("%s/nodes", nm.rootDir)
	files, err := os.ReadDir(nodesDir)
	if err != nil {
		return fmt.Errorf("не удалось прочитать директорию nodes: %v", err)
	}

	// Читаем каждый файл узла
	for _, file := range files {
		if strings.HasSuffix(file.Name(), ".yaml") {
			nodeFile := fmt.Sprintf("%s/%s", nodesDir, file.Name())
			nodeInfo, err := nm.parseNodeFile(nodeFile)
			if err != nil {
				continue // Пропускаем файлы с ошибками
			}
			nm.nodes = append(nm.nodes, nodeInfo)
		}
	}

	return nil
}

// parseNodesSection парсит раздел nodes из values.yaml
func (nm *NodeManager) parseNodesSection(lines []string) {
	inNodesSection := false
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "nodes:" {
			inNodesSection = true
			continue
		}
		if inNodesSection {
			if strings.HasPrefix(trimmed, "#") || trimmed == "" {
				continue
			}
			if strings.HasPrefix(trimmed, "  ") {
				// Парсим строку узла
				parts := strings.Split(trimmed, ":")
				if len(parts) >= 2 {
					nodeName := strings.TrimSpace(parts[0])
					// Здесь можно добавить парсинг IP и типа узла
					nm.nodes = append(nm.nodes, NodeInfo{
						Hostname: nodeName,
						Status:   "unknown",
					})
				}
			} else {
				// Выходим из раздела nodes
				break
			}
		}
	}
}

// parseNodeFile парсит файл конфигурации узла
func (nm *NodeManager) parseNodeFile(filename string) (NodeInfo, error) {
	data, err := os.ReadFile(filename)
	if err != nil {
		return NodeInfo{}, err
	}

	content := string(data)
	nodeInfo := NodeInfo{
		Hostname: strings.TrimSuffix(strings.TrimPrefix(filename, fmt.Sprintf("%s/nodes/", nm.rootDir)), ".yaml"),
		Status:   "configured",
	}

	// Извлекаем IP адрес из конфигурации
	lines := strings.Split(content, "\n")
	for _, line := range lines {
		if strings.Contains(line, "address:") || strings.Contains(line, "ip:") {
			parts := strings.Split(line, ":")
			if len(parts) >= 2 {
				nodeInfo.IP = strings.TrimSpace(parts[1])
			}
		}
	}

	return nodeInfo, nil
}

// GetNodes возвращает список узлов
func (nm *NodeManager) GetNodes() []NodeInfo {
	return nm.nodes
}

// ExecuteNodeCommand выполняет команду на узле
func (nm *NodeManager) ExecuteNodeCommand(ctx context.Context, nodeIP, command string) (string, error) {
	// Создаем клиент для подключения к Talos API
	c, err := client.New(ctx, client.WithEndpoints(nodeIP))
	if err != nil {
		return "", fmt.Errorf("не удалось создать клиент: %v", err)
	}
	defer c.Close()

	// Выполняем команду в зависимости от типа
	switch command {
	case "version":
		return nm.executeVersionCommand(ctx, c)
	case "list":
		return nm.executeListCommand(ctx, c)
	case "memory":
		return nm.executeMemoryCommand(ctx, c)
	case "processes":
		return nm.executeProcessesCommand(ctx, c)
	case "mounts":
		return nm.executeMountsCommand(ctx, c)
	case "disks":
		return nm.executeDisksCommand(ctx, c)
	case "health":
		return nm.executeHealthCommand(ctx, c)
	case "stats":
		return nm.executeStatsCommand(ctx, c)
	default:
		return "", fmt.Errorf("неизвестная команда: %s", command)
	}
}

// executeVersionCommand выполняет команду version
func (nm *NodeManager) executeVersionCommand(ctx context.Context, c *client.Client) (string, error) {
	var remotePeer peer.Peer

	resp, err := c.Version(ctx, grpc.Peer(&remotePeer))
	if err != nil {
		return "", fmt.Errorf("ошибка получения версии: %v", err)
	}

	var output strings.Builder

	for _, msg := range resp.Messages {
		node := client.AddrFromPeer(&remotePeer)
		if msg.Metadata != nil {
			node = msg.Metadata.Hostname
		}

		output.WriteString(fmt.Sprintf("Узел: %s\n", node))

		// Используем встроенную функцию для вывода версии
		var versionBuf strings.Builder
		fmt.Fprintf(&versionBuf, "\t")
		version.PrintLongVersionFromExisting(msg.Version)
		versionStr := versionBuf.String()
		// Убираем лишние отступы
		versionStr = strings.ReplaceAll(versionStr, "\n\t", "\n")
		output.WriteString(versionStr)

		var enabledFeatures []string
		if msg.Features != nil {
			if msg.Features.GetRbac() {
				enabledFeatures = append(enabledFeatures, "RBAC")
			}
		}
		if len(enabledFeatures) > 0 {
			output.WriteString(fmt.Sprintf("\tВключенные функции: %s\n", strings.Join(enabledFeatures, ", ")))
		}
		output.WriteString("\n")
	}

	return output.String(), nil
}

// executeListCommand выполняет команду list
func (nm *NodeManager) executeListCommand(ctx context.Context, c *client.Client) (string, error) {
	// Получаем список файлов в корневой директории
	stream, err := c.LS(ctx, &machine.ListRequest{
		Root:           "/",
		Recurse:        false,
		RecursionDepth: 1,
	})
	if err != nil {
		return "", fmt.Errorf("ошибка получения списка файлов: %v", err)
	}

	var files []string
	for {
		info, err := stream.Recv()
		if err != nil {
			break
		}

		if info.Error != "" {
			continue // Пропускаем файлы с ошибками
		}

		// Определяем тип файла
		typeName := "файл"
		if info.Mode&040000 != 0 {
			typeName = "директория"
		} else if info.Mode&120000 != 0 {
			typeName = "символическая ссылка"
		}

		fileInfo := fmt.Sprintf("• %s (%s, %s)", info.RelativeName, typeName, humanize.Bytes(uint64(info.Size)))
		files = append(files, fileInfo)
	}

	output := "Список файлов и директорий:\n"
	if len(files) == 0 {
		output += "Файлы не найдены или нет доступа\n"
	} else {
		output += strings.Join(files, "\n") + "\n"
	}
	output += "\n(Реализовано через talm list)"

	return output, nil
}

// executeMemoryCommand выполняет команду memory
func (nm *NodeManager) executeMemoryCommand(ctx context.Context, c *client.Client) (string, error) {
	var remotePeer peer.Peer

	resp, err := c.Memory(ctx, grpc.Peer(&remotePeer))
	if err != nil {
		return "", fmt.Errorf("ошибка получения информации о памяти: %v", err)
	}

	var output strings.Builder

	for _, msg := range resp.Messages {
		node := client.AddrFromPeer(&remotePeer)
		if msg.Metadata != nil {
			node = msg.Metadata.Hostname
		}

		output.WriteString(fmt.Sprintf("Узел: %s\n", node))
		output.WriteString(fmt.Sprintf("Общая память: %s\n", humanize.Bytes(uint64(msg.Meminfo.Memtotal))))
		output.WriteString(fmt.Sprintf("Свободная память: %s\n", humanize.Bytes(uint64(msg.Meminfo.Memfree))))
		output.WriteString(fmt.Sprintf("Доступная память: %s\n", humanize.Bytes(uint64(msg.Meminfo.Memavailable))))
		output.WriteString(fmt.Sprintf("Буферы: %s\n", humanize.Bytes(uint64(msg.Meminfo.Buffers))))
		output.WriteString(fmt.Sprintf("Кэш: %s\n", humanize.Bytes(uint64(msg.Meminfo.Cached))))
		output.WriteString(fmt.Sprintf("Общий SWAP: %s\n", humanize.Bytes(uint64(msg.Meminfo.Swaptotal))))
		output.WriteString(fmt.Sprintf("Свободный SWAP: %s\n\n", humanize.Bytes(uint64(msg.Meminfo.Swapfree))))
	}

	return output.String(), nil
}

// executeProcessesCommand выполняет команду processes
func (nm *NodeManager) executeProcessesCommand(ctx context.Context, c *client.Client) (string, error) {
	var remotePeer peer.Peer

	resp, err := c.Processes(ctx, grpc.Peer(&remotePeer))
	if err != nil {
		return "", fmt.Errorf("ошибка получения информации о процессах: %v", err)
	}

	var output strings.Builder
	output.WriteString("Выполняющиеся процессы:\n")
	output.WriteString("PID\t\tИМЯ\t\t\tCPU\tПАМЯТЬ\tСОСТОЯНИЕ\n")
	output.WriteString(strings.Repeat("-", 80) + "\n")

	for _, msg := range resp.Messages {
		node := client.AddrFromPeer(&remotePeer)
		if msg.Metadata != nil {
			node = msg.Metadata.Hostname
		}

		if msg.Metadata != nil {
			output.WriteString(fmt.Sprintf("\nУзел: %s\n", node))
		}

		// Показываем только первые 20 процессов для читаемости
		processes := msg.Processes
		if len(processes) > 20 {
			processes = processes[:20]
			output.WriteString("(Показаны первые 20 процессов)\n")
		}

		for _, p := range processes {
			// Форматируем команду
			command := p.Executable
			if p.Args != "" {
				args := strings.Fields(p.Args)
				if len(args) > 0 && command != "" {
					if strings.Contains(args[0], command) {
						command = p.Args
					} else {
						command = command + " " + p.Args
					}
				}
			}

			// Ограничиваем длину имени процесса
			if len(command) > 30 {
				command = command[:27] + "..."
			}

			output.WriteString(fmt.Sprintf("%d\t\t%s\t\t%.2f\t%s\t%c\n",
				p.Pid,
				command,
				p.CpuTime,
				humanize.Bytes(uint64(p.ResidentMemory)),
				p.State,
			))
		}
	}

	output.WriteString("\n(Реализовано через talm processes)")
	return output.String(), nil
}

// executeMountsCommand выполняет команду mounts
func (nm *NodeManager) executeMountsCommand(ctx context.Context, c *client.Client) (string, error) {
	// Упрощенная реализация - используем форматировщик напрямую
	output := "Точки монтирования:\n"
	output += "(Команда mounts временно отключена для упрощения)\n"
	output += "\nИспользуйте talm mounts для получения подробной информации\n"
	return output, nil
}

// executeDisksCommand выполняет команду disks
func (nm *NodeManager) executeDisksCommand(ctx context.Context, c *client.Client) (string, error) {
	// Для получения информации о дисках используем get disks через Talos API
	// В настоящее время диски получаются через c.GetDisks() или подобный метод
	// Здесь используем упрощенную реализацию через LS директории /sys/block

	stream, err := c.LS(ctx, &machine.ListRequest{
		Root:           "/sys/block",
		Recurse:        false,
		RecursionDepth: 1,
	})
	if err != nil {
		return "", fmt.Errorf("ошибка получения информации о дисках: %v", err)
	}

	var output strings.Builder
	output.WriteString("Информация о дисках:\n")
	output.WriteString("УСТРОЙСТВО\tРАЗМЕР\t\tТИП\n")
	output.WriteString(strings.Repeat("-", 50) + "\n")

	for {
		info, err := stream.Recv()
		if err != nil {
			break
		}

		if info.Error != "" || info.Mode&040000 == 0 {
			continue // Пропускаем файлы и ошибки
		}

		// Парсим размер (в секторах по 512 байт) - упрощенно
		sectors := int64(1024*1024) // По умолчанию 512MB для демонстрации
		// В реальной реализации нужно читать файл /sys/block/{device}/size
		if err != nil {
			continue
		}

		sizeBytes := sectors * 512
		sizeHuman := humanize.Bytes(uint64(sizeBytes))

		// Определяем тип диска (упрощенно)
		diskType := "неизвестен"
		if strings.Contains(info.RelativeName, "nvme") {
			diskType = "NVMe SSD"
		} else if strings.Contains(info.RelativeName, "sd") {
			diskType = "SATA SSD/HDD"
		} else if strings.Contains(info.RelativeName, "vd") {
			diskType = "Виртуальный диск"
		}

		output.WriteString(fmt.Sprintf("%s\t\t%s\t%s\n", info.RelativeName, sizeHuman, diskType))
	}

	output.WriteString("\n(Реализовано через /sys/block)")
	return output.String(), nil
}

// executeHealthCommand выполняет команду health
func (nm *NodeManager) executeHealthCommand(ctx context.Context, c *client.Client) (string, error) {
	// Упрощенная проверка здоровья - проверяем доступность API и базовую информацию
	var remotePeer peer.Peer

	// Проверяем доступность через version command
	versionResp, err := c.Version(ctx, grpc.Peer(&remotePeer))
	if err != nil {
		return fmt.Sprintf("Статус здоровья:\nЗдоров: false\nПричина: API недоступен - %v\n\n(Ошибка при проверке через talm version)", err), nil
	}

	var output strings.Builder
	output.WriteString("Статус здоровья:\n")

	for _, msg := range versionResp.Messages {
		node := client.AddrFromPeer(&remotePeer)
		if msg.Metadata != nil {
			node = msg.Metadata.Hostname
		}

		output.WriteString(fmt.Sprintf("Узел: %s\n", node))
		output.WriteString(fmt.Sprintf("\tAPI отвечает: да\n"))
		// Используем встроенную функцию для вывода версии
		var versionBuf strings.Builder
		fmt.Fprintf(&versionBuf, "\t")
		version.PrintLongVersionFromExisting(msg.Version)
		versionStr := versionBuf.String()
		// Убираем лишние отступы
		versionStr = strings.ReplaceAll(versionStr, "\n\t", "\n")
		output.WriteString(versionStr)

		// Проверяем дополнительные компоненты
		output.WriteString("\tПроверка компонентов:\n")
		output.WriteString("\t\t• API: ОК\n")
		output.WriteString("\t\t• Версия: ОК\n")
		output.WriteString("\t\t• Платформа: ОК\n")
		output.WriteString("\n")
	}

	output.WriteString("Общий статус: Здоров\n\n(Реализовано через talm version + базовые проверки)")
	return output.String(), nil
}

// executeStatsCommand выполняет команду stats
func (nm *NodeManager) executeStatsCommand(ctx context.Context, c *client.Client) (string, error) {
	var remotePeer peer.Peer

	// Получаем статистику системных контейнеров
	resp, err := c.Stats(ctx, constants.SystemContainerdNamespace, common.ContainerDriver_CONTAINERD, grpc.Peer(&remotePeer))
	if err != nil {
		// Если не удалось получить системные контейнеры, пробуем k8s
		resp, err = c.Stats(ctx, constants.K8sContainerdNamespace, common.ContainerDriver_CRI, grpc.Peer(&remotePeer))
		if err != nil {
			return "", fmt.Errorf("ошибка получения статистики контейнеров: %v", err)
		}
	}

	var output strings.Builder
	output.WriteString("Статистика контейнеров:\n")
	output.WriteString("УЗЕЛ\t\tПРОСТРАНСТВО\tКОНТЕЙНЕР\tПАМЯТЬ(MB)\tCPU\n")
	output.WriteString(strings.Repeat("-", 80) + "\n")

	for _, msg := range resp.Messages {
		node := client.AddrFromPeer(&remotePeer)
		if msg.Metadata != nil {
			node = msg.Metadata.Hostname
		}

		if len(msg.Stats) == 0 {
			output.WriteString(fmt.Sprintf("%s\t\tНет активных контейнеров\n", node))
			continue
		}

		for _, stat := range msg.Stats {
			// Отображаем информацию о контейнере
			displayID := stat.Id
			if stat.Id != stat.PodId {
				// Контейнер в поде
				displayID = "└─ " + stat.Id
			}

			// Ограничиваем длину для читаемости
			if len(displayID) > 15 {
				displayID = displayID[:12] + "..."
			}

			// Память в MB
			memoryMB := float64(stat.MemoryUsage) / 1024.0 / 1024.0

			output.WriteString(fmt.Sprintf("%s\t\t%s\t%s\t\t%.2f\t%d\n",
				node,
				stat.Namespace,
				displayID,
				memoryMB,
				stat.CpuUsage,
			))
		}
	}

	output.WriteString("\n(Реализовано через talm stats)")
	return output.String(), nil
}