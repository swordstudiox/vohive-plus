package api

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/swordstudiox/vohive-plus/internal/e911"
)

func e911ErrorStatus(err error) int {
	switch {
	case errors.Is(err, e911.ErrNotSupported), errors.Is(err, e911.ErrProviderUnavailable):
		return http.StatusBadRequest
	case errors.Is(err, e911.ErrIdentityUnavailable):
		return http.StatusConflict
	case errors.Is(err, e911.ErrChallengeIncomplete):
		return http.StatusNotImplemented
	case errors.Is(err, e911.ErrCarrierWebsheetAbsent):
		return http.StatusBadGateway
	default:
		return http.StatusInternalServerError
	}
}

func e911ErrorCode(err error) string {
	switch {
	case errors.Is(err, e911.ErrNotSupported):
		return "e911_not_supported"
	case errors.Is(err, e911.ErrIdentityUnavailable):
		return "e911_identity_unavailable"
	case errors.Is(err, e911.ErrProviderUnavailable):
		return "e911_provider_unavailable"
	case errors.Is(err, e911.ErrChallengeIncomplete):
		return "e911_challenge_not_implemented"
	case errors.Is(err, e911.ErrCarrierWebsheetAbsent):
		return "e911_websheet_unavailable"
	default:
		return "e911_start_failed"
	}
}

func (s *Server) handleDeviceE911Websheet(c *gin.Context) {
	coord := &e911.Coordinator{
		Pool:      s.pool,
		Websheets: s.websheets,
	}
	info, err := coord.StartWebsheet(c.Request.Context(), deviceIDParam(c))
	if err != nil {
		c.JSON(e911ErrorStatus(err), gin.H{"status": "error", "code": e911ErrorCode(err), "message": err.Error()})
		return
	}
	c.JSON(http.StatusCreated, info)
}
