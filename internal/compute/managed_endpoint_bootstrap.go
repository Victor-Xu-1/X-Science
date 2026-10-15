package compute

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
)

// ManagedEndpointBootstrap is only a captured comparison scope and the exact
// requested operation. It contains no registration scripts or credentials and
// grants no publication/execution authority without the canonical Store fence.
type ManagedEndpointBootstrap struct {
	Scope           ManagedEndpointPrivateCleanup `json:"scope"`
	RequestedAction ManagedEndpointActionRef      `json:"requestedAction"`
}

func ManagedEndpointBootstrapFor(instance ManagedEndpointOwnerInstance, requested ManagedEndpointActionRef) (ManagedEndpointBootstrap, error) {
	scope, err := ManagedEndpointPrivateCleanupFor(instance)
	if err != nil {
		return ManagedEndpointBootstrap{}, err
	}
	input := ManagedEndpointBootstrap{Scope: scope, RequestedAction: requested}
	return input, input.Validate()
}

func (input ManagedEndpointBootstrap) Validate() error {
	// Reuse the one immutable tuple validator; its cleanup spelling does not
	// grant this caller deletion authority or select a different runtime root.
	if _, err := input.Scope.Key(); err != nil {
		return err
	}
	if err := input.RequestedAction.Validate(); err != nil {
		return err
	}
	if input.RequestedAction.InstanceID != input.Scope.InstanceID {
		return ErrManagedEndpointStaleAction
	}
	return nil
}

func (input ManagedEndpointBootstrap) Matches(instance ManagedEndpointOwnerInstance) bool {
	scope, err := ManagedEndpointPrivateCleanupFor(instance)
	return err == nil && input.Scope == scope
}

func (input ManagedEndpointBootstrap) Encode() (string, error) {
	if err := input.Validate(); err != nil {
		return "", err
	}
	raw, err := json.Marshal(input)
	if err != nil {
		return "", err
	}
	if len(raw) > 16<<10 {
		return "", errors.New("managed endpoint bootstrap exceeds its size limit")
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

func ParseManagedEndpointBootstrap(encoded string) (ManagedEndpointBootstrap, error) {
	var input ManagedEndpointBootstrap
	if len(encoded) > base64.RawURLEncoding.EncodedLen(16<<10) {
		return input, errors.New("managed endpoint bootstrap exceeds its size limit")
	}
	raw, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		return input, err
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		return input, err
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return input, errors.New("managed endpoint bootstrap contains trailing data")
	}
	return input, input.Validate()
}
