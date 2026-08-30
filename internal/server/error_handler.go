package server

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/labstack/echo/v5"
)

type errorResponse struct {
	Message string `json:"message"`
}

func newHTTPErrorHandler() echo.HTTPErrorHandler {
	return func(c *echo.Context, err error) {
		if resp, unwrapErr := echo.UnwrapResponse(c.Response()); unwrapErr == nil && resp.Committed {
			return
		}

		_, status := echo.ResolveResponseStatus(c.Response(), err)
		if status <= 0 {
			status = http.StatusInternalServerError
		}
		message := http.StatusText(status)

		if httpErr, ok := errors.AsType[*echo.HTTPError](err); ok {
			message = httpErrorMessage(httpErr.Message, status)
		}

		if c.Request().Method == http.MethodHead {
			_ = c.NoContent(status)
			return
		}

		_ = c.JSON(status, errorResponse{Message: message})
	}
}

func httpErrorMessage(message any, status int) string {
	switch typed := message.(type) {
	case string:
		if typed != "" {
			return typed
		}
	case error:
		if typed.Error() != "" {
			return typed.Error()
		}
	case fmt.Stringer:
		if typed.String() != "" {
			return typed.String()
		}
	}

	if text := http.StatusText(status); text != "" {
		return text
	}

	return http.StatusText(http.StatusInternalServerError)
}
