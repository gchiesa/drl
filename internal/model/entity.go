// Package model defines the shared domain types used across the DRL service.
// These types are intentionally decoupled from any specific transport (HTTP,
// gRPC) so they can be reused by the admin API, the rate-limiting engine,
// the accounting subsystem, and cluster state-sync code.
package model

import (
	"fmt"
	"sort"
	"strings"

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
