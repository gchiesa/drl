package api

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"

	"github.com/gchiesa/drl/internal/model"
)

const (
	// headerMarker separates the URI path from the optional header list in the URL.
	headerMarker = "/_headers/"
)

// entityResponse is the JSON body returned by block / unblock endpoints.
type entityResponse struct {
	ID      string            `json:"id"`
	IP      string            `json:"ip"`
	URIPath string            `json:"uriPath"`
	Headers map[string]string `json:"headers"`
	Message string            `json:"message"`
	Errors  []string          `json:"errors"`
}

// blockedEntityEntry is one element in the GET /blocked-entity JSON array.
type blockedEntityEntry struct {
	ID        string            `json:"id"`
	IP        string            `json:"ip"`
	URIPath   string            `json:"uriPath"`
	Headers   map[string]string `json:"headers"`
	ExpiresAt string            `json:"expires_at"`
}

// parseEntityFromWildcard splits the Fiber wildcard parameter (everything after
// `/_path/`) into its constituent path and header map.
//
// Example wildcard: "api/v1/payments/_headers/User-Agent:ScraperBot,X-Bot:1"
//
//	→ path:    "api/v1/payments"
//	→ headers: {"User-Agent":"ScraperBot", "X-Bot":"1"}
func parseEntityFromWildcard(wildcard string) (path string, headers map[string]string, err error) {
	idx := strings.Index(wildcard, headerMarker)
	if idx >= 0 {
		path = strings.Clone(wildcard[:idx])
		headers, err = parseHeadersStr(wildcard[idx+len(headerMarker):])
	} else {
		path = strings.Clone(wildcard)
		headers = make(map[string]string)
	}

	if path == "" {
		err = fmt.Errorf("uriPath cannot be empty")
	}
	return
}

// parseHeadersStr parses "key:val,key2:val2" into a map.
// Returns an error if any pair is missing the colon separator or has an empty key.
func parseHeadersStr(s string) (map[string]string, error) {
	result := make(map[string]string)
	if s == "" {
		return result, nil
	}
	for _, pair := range strings.Split(s, ",") {
		kv := strings.SplitN(pair, ":", 2)
		if len(kv) != 2 {
			return nil, fmt.Errorf("malformed header pair: %q (expected key:value)", pair)
		}
		key := strings.Clone(strings.TrimSpace(kv[0]))
		val := strings.Clone(strings.TrimSpace(kv[1]))
		if key == "" {
			return nil, fmt.Errorf("empty header key in pair: %q", pair)
		}
		result[key] = val
	}
	return result, nil
}

// generateOperationID returns a monotonic timestamp string suitable for use as
// an operation identifier in responses.
func generateOperationID() string {
	return fmt.Sprintf("%d", time.Now().UnixNano())
}

// parseTTLQuery reads the optional "ttl" query parameter (seconds) and returns
// the requested duration. Falls back to fallback when the parameter is absent.
func parseTTLQuery(c *fiber.Ctx, fallback time.Duration) (time.Duration, error) {
	raw := c.Query("ttl")
	if raw == "" {
		return fallback, nil
	}
	secs, err := strconv.Atoi(raw)
	if err != nil || secs < 1 {
		return 0, fmt.Errorf("invalid ttl value: %q (must be a positive integer of seconds)", raw)
	}
	return time.Duration(secs) * time.Second, nil
}

// handleBlockEntityList handles GET /blocked-entity
// It returns all currently blocked entities from the local cache.
func (s *Server) handleBlockEntityList(c *fiber.Ctx) error {
	if s.blocklist == nil {
		return c.Status(fiber.StatusOK).JSON([]blockedEntityEntry{})
	}

	entries := s.blocklist.ListEntries()
	result := make([]blockedEntityEntry, 0, len(entries))

	for _, e := range entries {
		entry := blockedEntityEntry{
			ID:        e.Key,
			ExpiresAt: e.ExpiresAt.Format(time.RFC3339),
		}
		if e.Entity != nil {
			entry.IP = e.Entity.IP
			entry.URIPath = e.Entity.Path
			entry.Headers = e.Entity.Headers
		}
		result = append(result, entry)
	}

	return c.Status(fiber.StatusOK).JSON(result)
}

// handleBlockEntityAdd handles POST /blocked-entity/:ip/_path/*
// It adds the entity (IP + path + headers) to the local blocklist and
// asynchronously broadcasts the block event to all cluster peers.
func (s *Server) handleBlockEntityAdd(c *fiber.Ctx) error {
	ip := strings.Clone(c.Params("ip"))
	wildcard := c.Params("*")

	path, headers, err := parseEntityFromWildcard(wildcard)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(entityResponse{
			ID:     generateOperationID(),
			Errors: []string{err.Error()},
		})
	}

	ttl, err := parseTTLQuery(c, s.defaultBlockTTL)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(entityResponse{
			ID:     generateOperationID(),
			Errors: []string{err.Error()},
		})
	}

	entity := model.Entity{IP: ip, Path: path, Headers: headers}
	key := entity.Key()

	if s.blocklist != nil {
		s.blocklist.BlockWithMeta(key, ttl, &entity)
	}

	if s.broadcaster != nil {
		go func() {
			if qErr := s.broadcaster.QueueBlockEvent(key, ttl, &entity); qErr != nil {
				s.logger.Warn("failed to queue block broadcast",
					"error", qErr,
					"key", key,
				)
			}
		}()
	}

	s.logger.Info("entity blocked via admin API",
		"ip", ip,
		"path", path,
		"headers_count", len(headers),
		"key", key,
		"ttl", ttl,
	)

	return c.Status(fiber.StatusOK).JSON(entityResponse{
		ID:      generateOperationID(),
		IP:      ip,
		URIPath: path,
		Headers: headers,
		Message: "Entity added to blocklist",
		Errors:  []string{},
	})
}

// handleBlockEntityDelete handles DELETE /blocked-entity/:ip/_path/*
// It removes the entity from the local blocklist and asynchronously
// broadcasts the unblock event to all cluster peers.
func (s *Server) handleBlockEntityDelete(c *fiber.Ctx) error {
	ip := strings.Clone(c.Params("ip"))
	wildcard := c.Params("*")

	path, headers, err := parseEntityFromWildcard(wildcard)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(entityResponse{
			ID:     generateOperationID(),
			Errors: []string{err.Error()},
		})
	}

	entity := model.Entity{IP: ip, Path: path, Headers: headers}
	key := entity.Key()

	if s.blocklist != nil {
		s.blocklist.Unblock(key)
	}

	if s.broadcaster != nil {
		go func() {
			if qErr := s.broadcaster.QueueUnblockEvent(key); qErr != nil {
				s.logger.Warn("failed to queue unblock broadcast",
					"error", qErr,
					"key", key,
				)
			}
		}()
	}

	s.logger.Info("entity unblocked via admin API",
		"ip", ip,
		"path", path,
		"headers_count", len(headers),
		"key", key,
	)

	return c.Status(fiber.StatusOK).JSON(entityResponse{
		ID:      generateOperationID(),
		IP:      ip,
		URIPath: path,
		Headers: headers,
		Message: "Entity removed from blocklist",
		Errors:  []string{},
	})
}
