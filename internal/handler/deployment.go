package handler

import (
	"net/http"

	"Vylux/internal/deployment"

	"github.com/labstack/echo/v5"
)

type DeploymentHandler struct {
	target deployment.Target
}

func NewDeploymentHandler(target deployment.Target) *DeploymentHandler {
	return &DeploymentHandler{target: target}
}

// Handle serves the authenticated, machine-readable deployment contract.
func (h *DeploymentHandler) Handle(c *echo.Context) error {
	h.target.SetHeaders(c.Response().Header())
	if err := h.target.Validate(); err != nil {
		return echo.NewHTTPError(
			http.StatusServiceUnavailable,
			"deployment target is unavailable",
		).Wrap(err)
	}

	return c.JSON(http.StatusOK, h.target)
}
