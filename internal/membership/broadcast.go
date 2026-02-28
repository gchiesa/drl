package membership

import (
	"time"

	"github.com/hashicorp/memberlist"
	"github.com/vmihailenco/msgpack/v5"
)

// BroadcastEventType identifies the kind of user-level broadcast event.
type BroadcastEventType uint8

const (
	// BroadcastEventBlock signals that an entity should be added to the blocklist.
	BroadcastEventBlock BroadcastEventType = 1
	// BroadcastEventUnblock signals that an entity should be removed from the blocklist.
	BroadcastEventUnblock BroadcastEventType = 2
)

// BroadcastEvent is the payload carried in user-level memberlist broadcasts.
// Entity fields use `omitempty` for backward compatibility with older nodes.
type BroadcastEvent struct {
	Type       BroadcastEventType `msgpack:"type"`
	Key        string             `msgpack:"key"`
	TTL        time.Duration      `msgpack:"ttl,omitempty"`
	EntityIP   string             `msgpack:"entity_ip,omitempty"`
	EntityPath string             `msgpack:"entity_path,omitempty"`
	EntityHdrs map[string]string  `msgpack:"entity_hdrs,omitempty"`
}

// encodeBroadcastEvent serialises a BroadcastEvent to msgpack bytes.
func encodeBroadcastEvent(event BroadcastEvent) ([]byte, error) {
	return msgpack.Marshal(event)
}

// decodeBroadcastEvent deserialises a BroadcastEvent from msgpack bytes.
func decodeBroadcastEvent(data []byte) (BroadcastEvent, error) {
	var event BroadcastEvent
	err := msgpack.Unmarshal(data, &event)
	return event, err
}

// blocklistBroadcast implements memberlist.Broadcast for block/unblock events.
type blocklistBroadcast struct {
	data []byte
}

// Invalidates returns false — we never cancel a previously queued broadcast.
// A DELETE (unblock) event handles removal explicitly.
func (b *blocklistBroadcast) Invalidates(other memberlist.Broadcast) bool {
	return false
}

// Message returns the encoded broadcast payload.
func (b *blocklistBroadcast) Message() []byte {
	return b.data
}

// Finished is a no-op; we have nothing to clean up after transmission.
func (b *blocklistBroadcast) Finished() {}
