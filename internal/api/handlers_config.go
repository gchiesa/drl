package api

import (
	"fmt"

	"github.com/gofiber/fiber/v2"
)

// handleGetStaticConfig handles GET /configuration/static/:section
//
// Returns the JSON representation of the requested top-level configuration
// section. Protected by Digest authentication.
//
// Supported sections: accounting, membership, cache, listen, logging, internal-api
//
// Sensitive fields (e.g. encryption keys) are excluded from the response via
// json:"-" tags on the underlying config structs.
func (s *Server) handleGetStaticConfig(c *fiber.Ctx) error {
	section := c.Params("section")
	if section == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "section parameter is required",
		})
	}

	data, ok := s.staticConfig.GetConfigSection(section)
	if !ok {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{
			"error": fmt.Sprintf("config section %q not found; valid sections: accounting, membership, cache, listen, logging, internal-api", section),
		})
	}

	return c.JSON(data)
}
