package auth

import (
	"crypto/sha256"
	"crypto/subtle"
	"sync"
	"time"

	"github.com/qlqq/roomusic/backend/internal/identity"
)

type passwordEntry struct {
	hash  [32]byte
	proof [32]byte
	until time.Time
}

type passwordCache struct {
	mu         sync.Mutex
	entries    map[string]passwordEntry
	generation uint64
	nextSweep  time.Time
}

func (c *passwordCache) verify(id, hash, password string) bool {
	version := sha256.Sum256([]byte(hash))
	proof := sha256.Sum256([]byte(hash + "\x00" + password))
	now := time.Now()
	c.mu.Lock()
	if !now.Before(c.nextSweep) {
		for key, entry := range c.entries {
			if !now.Before(entry.until) {
				delete(c.entries, key)
			}
		}
		c.nextSweep = now.Add(time.Minute)
	}
	entry, found := c.entries[id]
	generation := c.generation
	c.mu.Unlock()
	if found && entry.hash == version && now.Before(entry.until) && subtle.ConstantTimeCompare(proof[:], entry.proof[:]) == 1 {
		return true
	}
	if !identity.VerifyPassword(hash, password) {
		return false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if generation == c.generation {
		if c.entries == nil {
			c.entries = make(map[string]passwordEntry)
		}
		c.entries[id] = passwordEntry{version, proof, now.Add(5 * time.Minute)}
	}
	return true
}

func (c *passwordCache) clear() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.generation++
	clear(c.entries)
}
func (c *passwordCache) forget(id string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.generation++
	delete(c.entries, id)
}
