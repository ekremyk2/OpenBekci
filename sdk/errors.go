package sdk

import "errors"

// Errors providers and importers map failures to. The core maps them to HTTP
// statuses and retry decisions without parsing provider codes; any other
// error from a pack is reported as pack_error (500) and logged.
//
// Wrap them with %w so the provider's own detail stays in the message.
var (
	// ErrInvalidSignature: a webhook's signature did not verify (401; the
	// event is not recorded).
	ErrInvalidSignature = errors.New("sdk: invalid signature")
	// ErrNotFound: the provider does not know the transaction (404).
	ErrNotFound = errors.New("sdk: not found")
	// ErrRejected: the provider rejected the request for a business reason
	// (declined card, refund over the paid amount): 422, not retried.
	ErrRejected = errors.New("sdk: rejected")
	// ErrUnavailable: a temporary failure on the provider's side (timeout,
	// 5xx): 503, the caller or the provider retries.
	ErrUnavailable = errors.New("sdk: provider unavailable")
	// ErrCredentials: the organization's provider credentials were refused
	// (422 on the settings that hold them).
	ErrCredentials = errors.New("sdk: credentials refused")
	// ErrUnsupported: the provider does not implement the operation (for
	// example partial refunds): 422.
	ErrUnsupported = errors.New("sdk: unsupported")
	// ErrNoRows is returned by Row.Scan of the tenant transaction when a query returned no row.
	ErrNoRows = errors.New("sdk: no rows")
)
