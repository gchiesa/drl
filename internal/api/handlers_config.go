package api

import (
	"fmt"

	"github.com/gofiber/fiber/v2"

	"github.com/gchiesa/drl/internal/api/models"
)

// handleGetStaticConfig handles GET /v1/configuration/static/:section.
//
// Returns the JSON representation of the requested top-level configuration
// section. Sensitive fields (e.g. encryption keys) are excluded via json:"-" tags.
//
// Supported sections: accounting, membership, cache, listen, logging, internal-api
//
// @Summary      Get static configuration section
// @Description  Returns the JSON representation of a named top-level configuration section.
// @Description  Sensitive fields (encryption keys, passwords) are redacted via json:\"-\" tags.
// @Description  Valid sections: accounting, membership, cache, listen, logging, internal-api
// @Tags         configuration
// @Produce      json
// @Param        section  path      string  true  "Configuration section name"  Enums(accounting,membership,cache,listen,logging,internal-api)
// @Success      200      {object}  object
// @Failure      400      {object}  models.ErrorResponse
// @Failure      401      {object}  models.ErrorResponse
// @Failure      404      {object}  models.ErrorResponse
// @Security     DigestAuth
// @Security     BearerToken
// @Router       /configuration/static/{section} [get]
func (s *Server) handleGetStaticConfig(c *fiber.Ctx) error {
	section := c.Params("section")
	if section == "" {
		return c.Status(fiber.StatusBadRequest).JSON(models.ErrorResponse{
			Error: "section parameter is required",
			Code:  fiber.StatusBadRequest,
		})
	}

	data, ok := s.staticConfig.GetConfigSection(section)
	if !ok {
		return c.Status(fiber.StatusNotFound).JSON(models.ErrorResponse{
			Error:   fmt.Sprintf("config section %q not found", section),
			Code:    fiber.StatusNotFound,
			Details: "valid sections: accounting, membership, cache, listen, logging, internal-api",
		})
	}

	return c.JSON(data)
}
