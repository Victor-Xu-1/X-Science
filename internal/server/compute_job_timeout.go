package server

import (
	"encoding/json"
	"errors"
	"math"
	"strconv"
	"time"
)

// No supplied deadline means no product-imposed wall-clock deadline. Positive
// operator/provider limits still constrain the selected execution entity.
func computeJobTimeout(input map[string]any, providerLimit *int, physicalLimit time.Duration) (time.Duration, error) {
	if physicalLimit < 0 || (providerLimit != nil && *providerLimit < 0) {
		return 0, errors.New("compute provider timeout must be nonnegative")
	}
	limit := physicalLimit
	if providerLimit != nil && *providerLimit > 0 {
		seconds := int64(*providerLimit)
		if seconds > math.MaxInt64/int64(time.Second) {
			return 0, errors.New("compute provider timeout is not representable")
		}
		configured := time.Duration(seconds) * time.Second
		if limit == 0 || configured < limit {
			limit = configured
		}
	}
	value, found := input["timeout_seconds"]
	if !found {
		return limit, nil
	}
	var seconds float64
	switch value := value.(type) {
	case float64:
		seconds = value
	case int:
		seconds = float64(value)
	case int64:
		seconds = float64(value)
	case json.Number:
		var err error
		seconds, err = strconv.ParseFloat(string(value), 64)
		if err != nil {
			return 0, errors.New("compute timeout must be a finite nonnegative number")
		}
	default:
		return 0, errors.New("compute timeout must be a number")
	}
	if math.IsNaN(seconds) || math.IsInf(seconds, 0) || seconds < 0 || math.Trunc(seconds) != seconds || seconds >= float64(math.MaxInt64)/float64(time.Second) {
		return 0, errors.New("compute timeout must be finite, nonnegative, integral seconds and representable")
	}
	if seconds == 0 {
		return limit, nil
	}
	requested := time.Duration(seconds * float64(time.Second))
	if limit > 0 && requested > limit {
		return 0, errors.New("compute timeout exceeds the selected provider's execution limit")
	}
	return requested, nil
}
