// Package model defines the shared domain types used across the DRL service.
// These types are intentionally decoupled from any specific transport (HTTP,
// gRPC) so they can be reused by the admin API, the rate-limiting engine,
// the accounting subsystem, and cluster state-sync code.
package model

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/cespare/xxhash/v2"
)

// Entity represents a rate-limiting identity composed of an IP address, a URI
// path, and zero or more HTTP headers. The DRL blocklist and accounting
// subsystems use Entity.Key() as the canonical cache key.
type Entity struct {
	IP      string            `json:"ip"      msgpack:"ip"`
	Path    string            `json:"uriPath" msgpack:"path"`
	Headers map[string]string `json:"headers" msgpack:"headers"`
}

// Key returns a deterministic, order-independent cache key for this entity
// using xxHash-64. The canonical form is:
//
//	IP|Path[|HeaderKey:HeaderVal]...
//
// where header pairs are sorted lexicographically by key.
func (e Entity) Key() string {
	keys := make([]string, 0, len(e.Headers))
	for k := range e.Headers {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var sb strings.Builder
	sb.WriteString(e.IP)
	sb.WriteString("|")
	sb.WriteString(e.Path)
	for _, k := range keys {
		sb.WriteString("|")
		sb.WriteString(k)
		sb.WriteString(":")
		sb.WriteString(e.Headers[k])
	}

	hash := xxhash.Sum64String(sb.String())
	return fmt.Sprintf("%016x", hash)
}

// EntityKeyToHash converts a hexadecimal string to a uint64 value. Returns an error if the string is not valid hex.
func EntityKeyToHash(s string) (uint64, error) {
	i, err := strconv.ParseUint(s, 16, 64)
	return i, err
}

// BlockedEntityInfo is returned by the blocklist cache when listing entries.
// It pairs the cache key with its expiration and the optional originating
// entity metadata (populated only for admin-API blocks, nil for automatic
// rate-limiter blocks).
type BlockedEntityInfo struct {
	Key       string
	ExpiresAt time.Time
	Entity    *Entity
}
