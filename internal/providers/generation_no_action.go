package providers

import (
	"errors"
	"sync/atomic"
)

// This is decoder-owned generation activity without a completed public/action
// response. Retain the original resource/idle cause and native boundary proof;
// it is neither transport unavailability nor durable task progress.
type providerGenerationNoActionError struct {
	cause                error
	toolBoundaryObserved *atomic.Bool
}

func (err *providerGenerationNoActionError) Error() string {
	return "provider generation reached its idle or response-byte boundary without completing a public response or tool action"
}
func (err *providerGenerationNoActionError) Unwrap() error { return err.cause }

func newProviderGenerationNoAction(cause error, native *atomic.Bool) error {
	err := &providerGenerationNoActionError{cause: cause, toolBoundaryObserved: native}
	return &providerEmptyResponseError{message: err.Error(), cause: err}
}

func IsProviderGenerationNoAction(err error) bool {
	var boundary *providerGenerationNoActionError
	return errors.As(err, &boundary)
}

// Failed provider-default budget hints require the request-scoped proof that
// no native boundary was observed. Partial tool arguments do not qualify.
func IsProviderGenerationNoNativeAction(err error) bool {
	var boundary *providerGenerationNoActionError
	return IsProviderEmptyResponse(err) && errors.As(err, &boundary) && boundary != nil &&
		boundary.toolBoundaryObserved != nil && !boundary.toolBoundaryObserved.Load()
}
