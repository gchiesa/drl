package membership

import "github.com/hashicorp/memberlist"

// blocklistBroadcast implements memberlist.Broadcast for DrlMessage payloads
// (block/unblock events) distributed via the gossip protocol.
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
