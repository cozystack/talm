package initwizard

import (
	"sync"
	"time"
)

// CacheEntry представляет запись в кэше
type CacheEntry struct {
	Value     interface{}
	ExpiresAt time.Time
	CreatedAt time.Time
}

// Cache представляет простой in-memory кэш с TTL
type Cache struct {
	data   map[string]*CacheEntry
	mutex  sync.RWMutex
	ttl    time.Duration
	evicted int64
	hits   int64
	misses int64
}

// NewCache создает новый экземпляр кэша
func NewCache(ttl time.Duration) *Cache {
	return &Cache{
		data: make(map[string]*CacheEntry),
		ttl:  ttl,
	}
}

// Get получает значение из кэша
func (c *Cache) Get(key string) (interface{}, bool) {
	c.mutex.RLock()
	entry, exists := c.data[key]
	c.mutex.RUnlock()

	if !exists {
		c.misses++
		return nil, false
	}

	// Проверяем TTL
	if time.Now().After(entry.ExpiresAt) {
		c.mutex.Lock()
		delete(c.data, key)
		c.mutex.Unlock()
		c.evicted++
		c.misses++
		return nil, false
	}

	c.hits++
	return entry.Value, true
}

// Set устанавливает значение в кэш
func (c *Cache) Set(key string, value interface{}) {
	c.mutex.Lock()
	defer c.mutex.Unlock()

	c.data[key] = &CacheEntry{
		Value:     value,
		ExpiresAt: time.Now().Add(c.ttl),
		CreatedAt: time.Now(),
	}
}

// Delete удаляет запись из кэша
func (c *Cache) Delete(key string) {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	delete(c.data, key)
}

// Clear очищает весь кэш
func (c *Cache) Clear() {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	c.data = make(map[string]*CacheEntry)
}

// Size возвращает размер кэша
func (c *Cache) Size() int {
	c.mutex.RLock()
	defer c.mutex.RUnlock()
	return len(c.data)
}

// Stats возвращает статистику кэша
func (c *Cache) Stats() (hits, misses, evicted, size int64) {
	c.mutex.RLock()
	defer c.mutex.RUnlock()
	return c.hits, c.misses, c.evicted, int64(len(c.data))
}

// Cleanup удаляет истекшие записи
func (c *Cache) Cleanup() {
	c.mutex.Lock()
	defer c.mutex.Unlock()

	now := time.Now()
	for key, entry := range c.data {
		if now.After(entry.ExpiresAt) {
			delete(c.data, key)
			c.evicted++
		}
	}
}

// StartCleanup запускает автоматическую очистку кэша
func (c *Cache) StartCleanup(interval time.Duration) {
	ticker := time.NewTicker(interval)
	go func() {
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				c.Cleanup()
			}
		}
	}()
}

// NodeCache специализированный кэш для информации о нодах
type NodeCache struct {
	cache     *Cache
	nodeMutex sync.RWMutex
	nodes     map[string]NodeInfo // Кэш последней информации о ноде
}

// NewNodeCache создает новый кэш нод
func NewNodeCache(ttl time.Duration) *NodeCache {
	return &NodeCache{
		cache: NewCache(ttl),
		nodes: make(map[string]NodeInfo),
	}
}

// GetNodeInfo получает информацию о ноде из кэша
func (nc *NodeCache) GetNodeInfo(ip string) (NodeInfo, bool) {
	// Сначала проверяем кэш в памяти
	nc.nodeMutex.RLock()
	node, exists := nc.nodes[ip]
	nc.nodeMutex.RUnlock()

	if exists {
		return node, true
	}

	// Проверяем общий кэш
	if cached, found := nc.cache.Get("node:" + ip); found {
		if nodeInfo, ok := cached.(NodeInfo); ok {
			// Сохраняем в локальный кэш
			nc.nodeMutex.Lock()
			nc.nodes[ip] = nodeInfo
			nc.nodeMutex.Unlock()
			return nodeInfo, true
		}
	}

	return NodeInfo{}, false
}

// SetNodeInfo сохраняет информацию о ноде в кэш
func (nc *NodeCache) SetNodeInfo(ip string, node NodeInfo) {
	nc.nodeMutex.Lock()
	nc.nodes[ip] = node
	nc.nodeMutex.Unlock()
	
	nc.cache.Set("node:"+ip, node)
}

// InvalidateNodeInfo удаляет информацию о ноде из кэша
func (nc *NodeCache) InvalidateNodeInfo(ip string) {
	nc.nodeMutex.Lock()
	delete(nc.nodes, ip)
	nc.nodeMutex.Unlock()
	
	nc.cache.Delete("node:" + ip)
}

// HardwareCache специализированный кэш для информации об оборудовании
type HardwareCache struct {
	cache *Cache
}

// NewHardwareCache создает новый кэш оборудования
func NewHardwareCache(ttl time.Duration) *HardwareCache {
	return &HardwareCache{
		cache: NewCache(ttl),
	}
}

// GetHardwareInfo получает информацию об оборудовании из кэша
func (hc *HardwareCache) GetHardwareInfo(ip string) (Hardware, bool) {
	if cached, found := hc.cache.Get("hardware:" + ip); found {
		if hardware, ok := cached.(Hardware); ok {
			return hardware, true
		}
	}
	return Hardware{}, false
}

// SetHardwareInfo сохраняет информацию об оборудовании в кэш
func (hc *HardwareCache) SetHardwareInfo(ip string, hardware Hardware) {
	hc.cache.Set("hardware:"+ip, hardware)
}

// InvalidateHardwareInfo удаляет информацию об оборудовании из кэша
func (hc *HardwareCache) InvalidateHardwareInfo(ip string) {
	hc.cache.Delete("hardware:" + ip)
}

// ConfigCache кэш для конфигурационных файлов
type ConfigCache struct {
	cache *Cache
}

// NewConfigCache создает новый кэш конфигураций
func NewConfigCache(ttl time.Duration) *ConfigCache {
	return &ConfigCache{
		cache: NewCache(ttl),
	}
}

// GetConfig получает конфигурацию из кэша
func (cc *ConfigCache) GetConfig(clusterName, configType string) (interface{}, bool) {
	key := clusterName + ":" + configType
	return cc.cache.Get(key)
}

// SetConfig сохраняет конфигурацию в кэш
func (cc *ConfigCache) SetConfig(clusterName, configType string, config interface{}) {
	key := clusterName + ":" + configType
	cc.cache.Set(key, config)
}

// InvalidateClusterConfig удаляет все конфигурации кластера из кэша
func (cc *ConfigCache) InvalidateClusterConfig(clusterName string) {
	// Упрощенная реализация - в реальном приложении можно использовать префиксы
	// для более эффективного удаления
	cc.cache.Clear()
}