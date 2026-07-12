package providers

import (
	"context"
	"errors"
	"net"
	"regexp"
	"strings"
)

const (
	ErrorProviderTimeout         = "provider_timeout"
	ErrorProviderRateLimited     = "provider_rate_limited"
	ErrorProviderUnavailable     = "provider_unavailable"
	ErrorProviderAuth            = "provider_auth"
	ErrorProviderNotConfigured   = "provider_not_configured"
	ErrorProviderUnsupported     = "provider_unsupported"
	ErrorProviderRejected        = "provider_rejected"
	ErrorProviderInvalidResponse = "provider_invalid_response"
	ErrorRequestCanceled         = "request_canceled"
)

var providerStatusPattern = regexp.MustCompile(`status\s+(\d{3})`)

// SafeErrorCode classifies provider failures without exposing request URLs,
// credentials, response bodies, or transport-specific error strings.
func SafeErrorCode(err error) string {
	if err == nil {
		return ""
	}
	if errors.Is(err, context.Canceled) {
		return ErrorRequestCanceled
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return ErrorProviderTimeout
	}
	var networkErr net.Error
	if errors.As(err, &networkErr) && networkErr.Timeout() {
		return ErrorProviderTimeout
	}
	if errors.Is(err, ErrNotConfigured) {
		return ErrorProviderNotConfigured
	}
	var validationErr *SummaryValidationError
	if errors.As(err, &validationErr) {
		return ErrorProviderInvalidResponse
	}
	lower := strings.ToLower(err.Error())
	if strings.Contains(lower, "unsupported") {
		return ErrorProviderUnsupported
	}
	if match := providerStatusPattern.FindStringSubmatch(lower); len(match) == 2 {
		switch match[1] {
		case "401", "403":
			return ErrorProviderAuth
		case "408", "504":
			return ErrorProviderTimeout
		case "429":
			return ErrorProviderRateLimited
		case "400", "404", "409", "413", "422":
			return ErrorProviderRejected
		case "500", "502", "503":
			return ErrorProviderUnavailable
		}
	}
	if strings.Contains(lower, "decode") || strings.Contains(lower, "malformed") || strings.Contains(lower, "no content") {
		return ErrorProviderInvalidResponse
	}
	return ErrorProviderUnavailable
}

func SafeErrorMessage(err error) string {
	switch SafeErrorCode(err) {
	case ErrorProviderTimeout:
		return "The model provider timed out. Arivu used a safe fallback where possible."
	case ErrorProviderRateLimited:
		return "The model provider rate limit was reached."
	case ErrorProviderAuth:
		return "The model provider rejected the configured credentials."
	case ErrorProviderNotConfigured:
		return "The model provider is not configured."
	case ErrorProviderUnsupported:
		return "The configured model provider does not support this operation."
	case ErrorProviderRejected:
		return "The model provider rejected the request."
	case ErrorProviderInvalidResponse:
		return "The model provider returned an invalid response."
	case ErrorRequestCanceled:
		return "The model request was canceled."
	default:
		return "The model provider is temporarily unavailable."
	}
}
