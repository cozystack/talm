package initwizard

import (
	"sync"
	"time"
)

// CacheEntry represents a cache entry
type CacheEntry struct {
	Value     interface{}
	ExpiresAt time.Time
	CreatedAt time.Time
}

// Cache represents a simple in-memory cache with TTL
type Cache struct {
	data   map[string]*CacheEntry
	mutex  sync.RWMutex
	ttl    time.Duration
	evicted int64
	hits   int64
	misses int64
}

// NewCache creates a new cache instance
func NewCache(ttl time.Duration) *Cache {
	return &Cache{
		data: make(map[string]*CacheEntry),
		ttl:  ttl,
	}
}

// Get retrieves a value from the cache
func (c *Cache) Get(key string) (interface{}, bool) {
	c.mutex.RLock()
	entry, exists := c.data[key]
	c.mutex.RUnlock()

	if !exists {
		c.misses++
		return nil, false
	}

	// Check TTL
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

// Set sets a value in the cache
func (c *Cache) Set(key string, value interface{}) {
	c.mutex.Lock()
	defer c.mutex.Unlock()

	c.data[key] = &CacheEntry{
		Value:     value,
		ExpiresAt: time.Now().Add(c.ttl),
		CreatedAt: time.Now(),
	}
}

// Delete removes an entry from the cache
func (c *Cache) Delete(key string) {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	delete(c.data, key)
}

// Clear clears the entire cache
func (c *Cache) Clear() {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	c.data = make(map[string]*CacheEntry)
}

// Size returns the cache size
func (c *Cache) Size() int {
	c.mutex.RLock()
	defer c.mutex.RUnlock()
	return len(c.data)
}

// Stats returns cache statistics
func (c *Cache) Stats() (hits, misses, evicted, size int64) {
	c.mutex.RLock()
	defer c.mutex.RUnlock()
	return c.hits, c.misses, c.evicted, int64(len(c.data))
}

// Cleanup removes expired entries
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

// StartCleanup starts automatic cache cleanup
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

// NodeCache specialized cache for node information
type NodeCache struct {
	cache     *Cache
	nodeMutex sync.RWMutex
	nodes     map[string]NodeInfo // Cache of the latest node information
}

// NewNodeCache creates a new node cache
func NewNodeCache(ttl time.Duration) *NodeCache {
	return &NodeCache{
		cache: NewCache(ttl),
		nodes: make(map[string]NodeInfo),
	}
}

// GetNodeInfo retrieves node information from the cache
func (nc *NodeCache) GetNodeInfo(ip string) (NodeInfo, bool) {
	// First check the in-memory cache
	nc.nodeMutex.RLock()
	node, exists := nc.nodes[ip]
	nc.nodeMutex.RUnlock()

	if exists {
		return node, true
	}

	// Check the general cache
	if cached, found := nc.cache.Get("node:" + ip); found {
		if nodeInfo, ok := cached.(NodeInfo); ok {
			// Save to local cache
			nc.nodeMutex.Lock()
			nc.nodes[ip] = nodeInfo
			nc.nodeMutex.Unlock()
			return nodeInfo, true
		}
	}

	return NodeInfo{}, false
}

// SetNodeInfo saves node information to the cache
func (nc *NodeCache) SetNodeInfo(ip string, node NodeInfo) {
	nc.nodeMutex.Lock()
	nc.nodes[ip] = node
	nc.nodeMutex.Unlock()
	
	nc.cache.Set("node:"+ip, node)
}

// InvalidateNodeInfo removes node information from the cache
func (nc *NodeCache) InvalidateNodeInfo(ip string) {
	nc.nodeMutex.Lock()
	delete(nc.nodes, ip)
	nc.nodeMutex.Unlock()
	
	nc.cache.Delete("node:" + ip)
}

// HardwareCache specialized cache for hardware information
type HardwareCache struct {
	cache *Cache
}

// NewHardwareCache creates a new hardware cache
func NewHardwareCache(ttl time.Duration) *HardwareCache {
	return &HardwareCache{
		cache: NewCache(ttl),
	}
}

// GetHardwareInfo retrieves hardware information from the cache
func (hc *HardwareCache) GetHardwareInfo(ip string) (Hardware, bool) {
	if cached, found := hc.cache.Get("hardware:" + ip); found {
		if hardware, ok := cached.(Hardware); ok {
			return hardware, true
		}
	}
	return Hardware{}, false
}

// SetHardwareInfo saves hardware information to the cache
func (hc *HardwareCache) SetHardwareInfo(ip string, hardware Hardware) {
	hc.cache.Set("hardware:"+ip, hardware)
}

// InvalidateHardwareInfo removes hardware information from the cache
func (hc *HardwareCache) InvalidateHardwareInfo(ip string) {
	hc.cache.Delete("hardware:" + ip)
}

// ConfigCache cache for configuration files
type ConfigCache struct {
	cache *Cache
}

// NewConfigCache creates a new configuration cache
func NewConfigCache(ttl time.Duration) *ConfigCache {
	return &ConfigCache{
		cache: NewCache(ttl),
	}
}

// GetConfig retrieves configuration from the cache
func (cc *ConfigCache) GetConfig(clusterName, configType string) (interface{}, bool) {
	key := clusterName + ":" + configType
	return cc.cache.Get(key)
}

// SetConfig saves configuration to the cache
func (cc *ConfigCache) SetConfig(clusterName, configType string, config interface{}) {
	key := clusterName + ":" + configType
	cc.cache.Set(key, config)
}

// InvalidateClusterConfig removes all cluster configurations from the cache
func (cc *ConfigCache) InvalidateClusterConfig(clusterName string) {
	// Simplified implementation - in a real application, prefixes can be used
	// для более эффективного удаления
	cc.cache.Clear()
}