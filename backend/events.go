package backend

import (
	"sync"
)

// QueueEvent is the per-user event payload broadcast via SSE.
// Kind is one of: "added", "started", "progress", "completed", "failed", "skipped".
type QueueEvent struct {
	Kind     string  `json:"kind"`
	ItemID   string  `json:"item_id"`
	Track    string  `json:"track,omitempty"`
	Artist   string  `json:"artist,omitempty"`
	Album    string  `json:"album,omitempty"`
	Status   string  `json:"status,omitempty"`
	Progress float64 `json:"progress_mb,omitempty"`
	Speed    float64 `json:"speed_mbps,omitempty"`
	Size     float64 `json:"size_mb,omitempty"`
	Error    string  `json:"error,omitempty"`
}

type subscriber struct {
	ch chan QueueEvent
}

var (
	itemOwners   = map[string]string{} // itemID → userID
	itemOwnersMu sync.RWMutex

	subscribers   = map[string][]*subscriber{} // userID → list
	subscribersMu sync.RWMutex
)

func recordOwner(itemID, userID string) {
	if itemID == "" || userID == "" {
		return
	}
	itemOwnersMu.Lock()
	itemOwners[itemID] = userID
	itemOwnersMu.Unlock()
}

func forgetOwner(itemID string) {
	itemOwnersMu.Lock()
	delete(itemOwners, itemID)
	itemOwnersMu.Unlock()
}

// OwnerForItem returns the userID that triggered the given itemID, or "" if unknown.
func OwnerForItem(itemID string) string {
	itemOwnersMu.RLock()
	defer itemOwnersMu.RUnlock()
	return itemOwners[itemID]
}

// Subscribe registers an SSE listener for events targeted at userID.
// The returned cancel function unsubscribes and closes the channel.
func Subscribe(userID string) (<-chan QueueEvent, func()) {
	sub := &subscriber{ch: make(chan QueueEvent, 16)}

	subscribersMu.Lock()
	subscribers[userID] = append(subscribers[userID], sub)
	subscribersMu.Unlock()

	cancel := func() {
		subscribersMu.Lock()
		list := subscribers[userID]
		for i, s := range list {
			if s == sub {
				subscribers[userID] = append(list[:i], list[i+1:]...)
				break
			}
		}
		if len(subscribers[userID]) == 0 {
			delete(subscribers, userID)
		}
		subscribersMu.Unlock()
		close(sub.ch)
	}
	return sub.ch, cancel
}

// publish fans out an event to all current subscribers of userID. Drops events
// for slow listeners rather than blocking the producer.
func publish(userID string, ev QueueEvent) {
	if userID == "" {
		return
	}
	subscribersMu.RLock()
	list := append([]*subscriber(nil), subscribers[userID]...)
	subscribersMu.RUnlock()
	for _, s := range list {
		select {
		case s.ch <- ev:
		default:
			// Listener too slow; drop event.
		}
	}
}
