package tesla

import (
	"context"
	"errors"
	"net/url"
	"time"
)

// A single retry, because Tesla bills the Fleet API per request. One more
// attempt covers a dropped connection without turning a bad minute into a
// doubled bill.
const (
	maxAttempts = 2
	retryDelay  = 500 * time.Millisecond
)

// transport reports whether err is a transport failure — the request never got
// an answer. Those are the only Fleet API failures worth repeating: a request
// Tesla answered has already been billed, so retrying its 500 pays twice for
// the same read.
//
// A cancelled or expired context is never retried; the caller has stopped
// caring about the answer.
func transport(ctx context.Context, err error) bool {
	if err == nil || ctx.Err() != nil {
		return false
	}
	var urlErr *url.Error
	return errors.As(err, &urlErr)
}
