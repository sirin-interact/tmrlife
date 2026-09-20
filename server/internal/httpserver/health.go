package httpserver

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/labstack/echo/v5"
)

const (
	healthzPath = "/healthz"
	readyzPath  = "/readyz"
)

type health struct {
	db      Pinger
	logger  *slog.Logger
	timeout time.Duration
}

type statusBody struct {
	Status string `json:"status"`
}

// live는 프로세스가 살아 있는지만 답한다.
// 여기서 DB를 보면 DB가 잠깐 끊겼을 때 멀쩡한 서버까지 재시작된다.
func (h *health) live(c *echo.Context) error {
	return c.JSON(http.StatusOK, statusBody{Status: "ok"})
}

// ready는 요청을 받을 준비가 됐는지 답한다. DB에 닿지 않으면 503으로 트래픽을 받지 않겠다고 알린다.
func (h *health) ready(c *echo.Context) error {
	if h.db == nil {
		return c.JSON(http.StatusServiceUnavailable, statusBody{Status: "unavailable"})
	}

	// 확인이 오래 걸리면 확인 요청이 쌓여 서버를 더 힘들게 한다. 짧게 끊는다.
	ctx, cancel := context.WithTimeout(c.Request().Context(), h.timeout)
	defer cancel()

	if err := h.db.Ping(ctx); err != nil {
		h.logger.LogAttrs(ctx, slog.LevelWarn, "readiness check failed",
			slog.String("dependency", "database"),
			slog.String("error", err.Error()),
		)
		return c.JSON(http.StatusServiceUnavailable, statusBody{Status: "unavailable"})
	}
	return c.JSON(http.StatusOK, statusBody{Status: "ready"})
}
