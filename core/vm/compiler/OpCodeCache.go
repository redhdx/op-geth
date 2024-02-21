package compiler

import (
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/log"
	"github.com/holiman/uint256"
	"sync"
)

// CodeCacheGCThreshold TODO: make codecache threshold configurable.
const CodeCacheGCThreshold = 1024 * 1024 * 1024 /* 1 GB */
// CodeCacheGCSoftLimit is used to trigger GC for memory control.
// upper limit of bytecodes of smart contract is ~25MB.
const CodeCacheGCSoftLimit = 200 * 1024 * 1024 /* 200MB */

type OptCode []byte

// ThreeU8Operands Aux struct for code fusion of 3 uint8 operands
type ThreeU8Operands struct {
	x, y, z uint8
}

type OpCodeCache struct {
	opcodesCache   map[common.Address]OptCode
	codeCacheMutex sync.RWMutex
	codeCacheSize  uint64
	/* map of shl and sub arguments and results*/
	shlAndSubMap      map[ThreeU8Operands]*uint256.Int
	shlAndSubMapMutex sync.RWMutex
}

func (c *OpCodeCache) GetCachedCode(address common.Address) OptCode {

	c.codeCacheMutex.RLock()

	processedCode, ok := c.opcodesCache[address]
	if !ok {
		processedCode = nil
	}
	c.codeCacheMutex.RUnlock()
	return processedCode
}

func (c *OpCodeCache) RemoveCachedCode(address common.Address) {
	c.codeCacheMutex.Lock()
	if c.opcodesCache == nil || c.codeCacheSize == 0 {
		c.codeCacheMutex.Unlock()
		return
	}
	_, ok := c.opcodesCache[address]
	if ok {
		delete(c.opcodesCache, address)
	}
	c.codeCacheMutex.Unlock()
}

func (c *OpCodeCache) UpdateCodeCache(address common.Address, code OptCode) error {

	c.codeCacheMutex.Lock()

	if c.codeCacheSize+CodeCacheGCSoftLimit > CodeCacheGCThreshold {
		log.Warn("Code cache GC triggered\n")
		// TODO: should we depends on Golang GC here?
		// TODO: the current implementation of clear all is not reasonable.
		// must have better algorithm such as LRU and should consider hot addresses such as ones in accesslist.
		for k := range c.opcodesCache {
			delete(c.opcodesCache, k)
		}
		c.codeCacheSize = 0
	}
	c.opcodesCache[address] = code
	c.codeCacheSize += uint64(len(code))
	c.codeCacheMutex.Unlock()
	return nil
}

func (c *OpCodeCache) CacheShlAndSubMap(x uint8, y uint8, z uint8, val *uint256.Int) {
	c.shlAndSubMapMutex.Lock()
	if c.shlAndSubMap[ThreeU8Operands{x, y, z}] == nil {
		c.shlAndSubMap[ThreeU8Operands{x, y, z}] = val
	}
	c.shlAndSubMapMutex.Unlock()
}

func (c *OpCodeCache) GetValFromShlAndSubMap(x uint8, y uint8, z uint8) *uint256.Int {
	c.shlAndSubMapMutex.RLock()
	val, ok := c.shlAndSubMap[ThreeU8Operands{x, y, z}]
	c.shlAndSubMapMutex.RUnlock()
	if !ok {
		return nil
	}
	return val
}

var once sync.Once
var opcodeCache *OpCodeCache

func GetOpCodeCacheInstance() *OpCodeCache {
	once.Do(func() {
		opcodeCache = &OpCodeCache{
			opcodesCache:   make(map[common.Address]OptCode, CodeCacheGCThreshold>>10),
			shlAndSubMap:   make(map[ThreeU8Operands]*uint256.Int, 4096),
			codeCacheMutex: sync.RWMutex{},
		}
	})
	return opcodeCache
}
