package model

import (
	"time"

	"github.com/taigrr/crush/internal/message"
)

const (
	previewCacheCapacity = 8
	previewCacheTTL      = 2 * time.Second
)

type previewKey struct {
	sessionID string
	root      string
}

type previewCacheEntry struct {
	key      previewKey
	msgs     []message.Message
	loadedAt time.Time
}

type previewCache struct {
	entries []previewCacheEntry
}

func (c *previewCache) get(key previewKey, now time.Time) ([]message.Message, bool) {
	for i := range c.entries {
		entry := c.entries[i]
		if entry.key != key {
			continue
		}
		if now.Sub(entry.loadedAt) >= previewCacheTTL {
			c.remove(i)
			return nil, false
		}
		c.promote(i)
		return cloneMessages(entry.msgs), true
	}
	return nil, false
}

func (c *previewCache) put(key previewKey, msgs []message.Message, now time.Time) {
	for i := range c.entries {
		if c.entries[i].key == key {
			c.remove(i)
			break
		}
	}
	c.entries = append(c.entries, previewCacheEntry{})
	copy(c.entries[1:], c.entries)
	c.entries[0] = previewCacheEntry{key: key, msgs: cloneMessages(msgs), loadedAt: now}
	if len(c.entries) <= previewCacheCapacity {
		return
	}
	c.entries[len(c.entries)-1] = previewCacheEntry{}
	c.entries = c.entries[:previewCacheCapacity]
}

func (c *previewCache) promote(i int) {
	if i == 0 {
		return
	}
	entry := c.entries[i]
	copy(c.entries[1:i+1], c.entries[:i])
	c.entries[0] = entry
}

func (c *previewCache) remove(i int) {
	copy(c.entries[i:], c.entries[i+1:])
	c.entries[len(c.entries)-1] = previewCacheEntry{}
	c.entries = c.entries[:len(c.entries)-1]
}

func cloneMessages(msgs []message.Message) []message.Message {
	cloned := make([]message.Message, len(msgs))
	for i := range msgs {
		cloned[i] = msgs[i].Clone()
	}
	return cloned
}
