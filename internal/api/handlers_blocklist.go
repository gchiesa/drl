package api

import (
	"fmt"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"

	"github.com/gchiesa/drl/internal/model"
)

const (
	// headerMarker separates the URI path from the optional header list in the URL.
	headerMarker = "/_headers/"
	// defaultBlockTTL is the time-to-live for manually created blocks.
	defaultBlockTTL = 24 * time.Hour
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
		path = wildcard[:idx]
		headers, err = parseHeadersStr(wildcard[idx+len(headerMarker):])
	} else {
		path = wildcard
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
		key := strings.TrimSpace(kv[0])
		val := strings.TrimSpace(kv[1])
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

// handleBlockEntityAdd handles POST /blocked-entity/:ip/_path/*
// It adds the entity (IP + path + headers) to the local blocklist and
// asynchronously broadcasts the block event to all cluster peers.
func (s *Server) handleBlockEntityAdd(c *fiber.Ctx) error {
	ip := c.Params("ip")
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
		s.blocklist.Block(key, defaultBlockTTL)
	}

	if s.broadcaster != nil {
		go func() {
			if qErr := s.broadcaster.QueueBlockEvent(key, defaultBlockTTL); qErr != nil {
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
	ip := c.Params("ip")
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
