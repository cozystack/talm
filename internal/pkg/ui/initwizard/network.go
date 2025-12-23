package initwizard

import (
	"context"
	"fmt"
	"net"
	"sync"
	"time"
)

// ConnectionPool представляет пул сетевых соединений
type ConnectionPool struct {
	connections map[string]net.Conn
	mutex       sync.RWMutex
	maxIdle     time.Duration
	maxLifetime time.Duration
	metrics     *PoolMetrics
}

// PoolMetrics метрики пула соединений
type PoolMetrics struct {
	Created    int64
	Reused     int64
	Closed     int64
	Active     int64
	GetCalls   int64
	PutCalls   int64
}

// NewConnectionPool создает новый пул соединений
func NewConnectionPool(maxIdle, maxLifetime time.Duration) *ConnectionPool {
	return &ConnectionPool{
		connections: make(map[string]net.Conn),
		maxIdle:     maxIdle,
		maxLifetime: maxLifetime,
		metrics:     &PoolMetrics{},
	}
}

// Get получает соединение из пула
func (p *ConnectionPool) Get(network, addr string) (net.Conn, error) {
	p.mutex.Lock()
	defer p.mutex.Unlock()
	p.metrics.GetCalls++

	key := network + ":" + addr
	
	// Проверяем существующие соединения
	if conn, exists := p.connections[key]; exists {
		// Проверяем, не истекло ли соединение
		if time.Since(conn.(*timedConn).lastUsed) < p.maxIdle {
			p.metrics.Reused++
			conn.(*timedConn).lastUsed = time.Now()
			return conn, nil
		}
		// Закрываем истекшее соединение
		conn.Close()
		delete(p.connections, key)
		p.metrics.Closed++
	}

	// Создаем новое соединение
	conn, err := net.Dial(network, addr)
	if err != nil {
		return nil, err
	}

	timedConn := &timedConn{
		Conn:      conn,
		created:   time.Now(),
		lastUsed:  time.Now(),
		network:   network,
		addr:      addr,
	}

	p.connections[key] = timedConn
	p.metrics.Created++
	p.metrics.Active++

	return timedConn, nil
}

// Put возвращает соединение в пул
func (p *ConnectionPool) Put(conn net.Conn) error {
	p.mutex.Lock()
	defer p.mutex.Unlock()
	p.metrics.PutCalls++

	if timedConn, ok := conn.(*timedConn); ok {
		key := timedConn.network + ":" + timedConn.addr
		
		// Проверяем lifetime соединения
		if time.Since(timedConn.created) > p.maxLifetime {
			conn.Close()
			delete(p.connections, key)
			p.metrics.Closed++
			p.metrics.Active--
			return nil
		}

		// Обновляем время использования
		timedConn.lastUsed = time.Now()
		p.connections[key] = timedConn
	}

	return nil
}

// Close закрывает все соединения в пуле
func (p *ConnectionPool) Close() error {
	p.mutex.Lock()
	defer p.mutex.Unlock()

	for key, conn := range p.connections {
		conn.Close()
		delete(p.connections, key)
		p.metrics.Closed++
	}
	p.metrics.Active = 0

	return nil
}

// GetMetrics возвращает метрики пула
func (p *ConnectionPool) GetMetrics() PoolMetrics {
	p.mutex.RLock()
	defer p.mutex.RUnlock()
	return *p.metrics
}

// Size возвращает текущий размер пула
func (p *ConnectionPool) Size() int {
	p.mutex.RLock()
	defer p.mutex.RUnlock()
	return len(p.connections)
}

// timedConn представляет соединение с метаданными
type timedConn struct {
	net.Conn
	created  time.Time
	lastUsed time.Time
	network  string
	addr     string
}

// NetworkClient клиент для сетевых операций с пулом соединений
type NetworkClient struct {
	pool     *ConnectionPool
	timeout  time.Duration
}

// NewNetworkClient создает нового сетевого клиента
func NewNetworkClient(pool *ConnectionPool, timeout time.Duration) *NetworkClient {
	return &NetworkClient{
		pool:    pool,
		timeout: timeout,
	}
}

// ExecuteWithConnection выполняет операцию с соединением из пула
func (nc *NetworkClient) ExecuteWithConnection(network, addr string, operation func(net.Conn) error) error {
	conn, err := nc.pool.Get(network, addr)
	if err != nil {
		return NewNetworkErrorWithCause(
			"NET_001", 
			"не удалось получить соединение", 
			fmt.Sprintf("сеть: %s, адрес: %s", network, addr), 
			err,
		)
	}
	defer nc.pool.Put(conn)

	// Устанавливаем таймаут
	if nc.timeout > 0 {
		conn.SetDeadline(time.Now().Add(nc.timeout))
		defer conn.SetDeadline(time.Time{})
	}

	return operation(conn)
}

// ScanWithPool сканирование с использованием пула соединений
func (nc *NetworkClient) ScanWithPool(ctx context.Context, cidr string, operation func(context.Context, string, net.Conn) error) error {
	// Упрощенная реализация - получаем список IP из CIDR
	_, ipNet, err := net.ParseCIDR(cidr)
	if err != nil {
		return NewNetworkErrorWithCause(
			"NET_002", 
			"некорректная CIDR нотация", 
			fmt.Sprintf("CIDR: %s", cidr), 
			err,
		)
	}

	// Создаем канал для ограничения параллелизма
	const maxWorkers = 10
	workerChan := make(chan struct{}, maxWorkers)
	defer close(workerChan)

	// Обрабатываем IP адреса
	for ip := ipNet.IP.Mask(ipNet.Mask); ipNet.Contains(ip); ip = nextIP(ip) {
		select {
		case <-ctx.Done():
			return WrapError(ctx.Err(), ErrNetwork, "NET_003", "сканирование отменено", "операция была отменена пользователем")
		case workerChan <- struct{}{}:
			go func(ip net.IP) {
				defer func() { <-workerChan }()
				
				addr := ip.String() + ":50000"
				err := nc.ExecuteWithConnection("tcp", addr, func(conn net.Conn) error {
					return operation(ctx, addr, conn)
				})
				if err != nil {
					// Логируем ошибку, но продолжаем
					// В реальном приложении можно добавить более продвинутую обработку
				}
			}(ip)
		}
	}

	return nil
}

// nextIP возвращает следующий IP адрес
func nextIP(ip net.IP) net.IP {
	next := make(net.IP, len(ip))
	copy(next, ip)
	for j := len(next) - 1; j >= 0; j-- {
		next[j]++
		if next[j] > 0 {
			break
		}
	}
	return next
}

// RateLimiter ограничитель скорости для сетевых операций
type RateLimiter struct {
	tokens    int
	capacity  int
	lastRefill time.Time
	mutex     sync.Mutex
}

// NewRateLimiter создает новый ограничитель скорости
func NewRateLimiter(capacity int) *RateLimiter {
	return &RateLimiter{
		capacity:   capacity,
		tokens:     capacity,
		lastRefill: time.Now(),
	}
}

// Allow проверяет, разрешена ли операция
func (rl *RateLimiter) Allow() bool {
	rl.mutex.Lock()
	defer rl.mutex.Unlock()

	now := time.Now()
	elapsed := now.Sub(rl.lastRefill)
	
	// Пополняем токены (1 токен в секунду)
	tokensToAdd := int(elapsed.Seconds())
	if tokensToAdd > 0 {
		rl.tokens = min(rl.capacity, rl.tokens+tokensToAdd)
		rl.lastRefill = now
	}

	if rl.tokens > 0 {
		rl.tokens--
		return true
	}

	return false
}

// min возвращает минимум из двух чисел
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}