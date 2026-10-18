package server

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"
	kernelruntime "x-science/internal/kernel"
	workspace "x-science/internal/persistence/workspace"
)

const manualNotebookRequestSchema = "x-science.manual-kernel-request.v1"

type manualNotebookExpectedTarget struct {
	FrameIncarnationID string `json:"expected_frame_incarnation_id"`
	RootFrameID        string `json:"expected_root_frame_id"`
	RootIncarnationID  string `json:"expected_root_frame_incarnation_id"`
	KernelID           string `json:"kernel_id"`
	KernelGeneration   uint64 `json:"expected_kernel_generation"`
	Language           string `json:"language"`
	Environment        string `json:"environment"`
}

type manualNotebookRequest struct {
	Schema    string `json:"schema"`
	RequestID string `json:"request_id"`
	manualNotebookExpectedTarget
	Code          string `json:"code"`
	TimeoutMillis int64  `json:"timeout_ms"`
}

type manualNotebookAPIError struct {
	status int
	code   string
	detail string
}

func (e *manualNotebookAPIError) Error() string { return e.detail }

func manualNotebookInvalid(detail string) error {
	return &manualNotebookAPIError{http.StatusBadRequest, "MANUAL_EXECUTION_INVALID_REQUEST", detail}
}

func validManualNotebookID(value string) bool {
	return value != "" && value == strings.TrimSpace(value) && len(value) <= 512 && utf8.ValidString(value) &&
		!strings.ContainsAny(value, "/\\\x00\r\n\t")
}

func validateManualNotebookRequestID(value string) bool {
	id, err := uuid.Parse(value)
	return err == nil && id.String() == value
}

func (target manualNotebookExpectedTarget) validate() error {
	for _, id := range []string{target.FrameIncarnationID, target.RootFrameID, target.RootIncarnationID, target.KernelID} {
		if !validManualNotebookID(id) {
			return manualNotebookInvalid("Complete frozen scope and kernel identity are required")
		}
	}
	if target.KernelGeneration == 0 || target.KernelGeneration > 9007199254740991 ||
		(target.Language != "python" && target.Language != "r") || !kernelruntime.ValidEnvironmentName(target.Environment) {
		return manualNotebookInvalid("A valid kernel generation, language and environment are required")
	}
	return nil
}

func (request manualNotebookRequest) validate() error {
	if request.Schema != manualNotebookRequestSchema || !validateManualNotebookRequestID(request.RequestID) ||
		!utf8.ValidString(request.Code) || strings.TrimSpace(request.Code) == "" || len(request.Code) > 100000 ||
		request.TimeoutMillis <= 0 || request.TimeoutMillis > userTerminalCellTimeout.Milliseconds() {
		return manualNotebookInvalid("Invalid schema, request UUID, source or bounded positive timeout")
	}
	return request.manualNotebookExpectedTarget.validate()
}

func (request manualNotebookRequest) storeRequest() workspace.ManualKernelRequest {
	return workspace.ManualKernelRequest{RequestID: request.RequestID, Source: request.Code,
		Language: request.Language, Environment: request.Environment, KernelID: request.KernelID,
		ExpectedKernelGeneration: request.KernelGeneration, TimeoutMillis: request.TimeoutMillis}
}

func decodeManualNotebookRequest(w http.ResponseWriter, r *http.Request) (manualNotebookRequest, error) {
	var request manualNotebookRequest
	if r.URL.RawQuery != "" {
		return request, manualNotebookInvalid("Submission and cancellation do not accept query parameters")
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	raw, err := io.ReadAll(r.Body)
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			return request, &manualNotebookAPIError{http.StatusRequestEntityTooLarge, "MANUAL_EXECUTION_BODY_TOO_LARGE", "Request body exceeds 1 MiB"}
		}
		return request, manualNotebookInvalid("Could not read request body")
	}
	if !utf8.Valid(raw) {
		return request, manualNotebookInvalid("Request body must be UTF-8")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&request) != nil {
		return request, manualNotebookInvalid("Invalid typed request body")
	}
	if decoder.Decode(new(any)) != io.EOF {
		return request, manualNotebookInvalid("Exactly one JSON request is required")
	}
	return request, request.validate()
}

func decodeManualNotebookLookup(r *http.Request) (manualNotebookExpectedTarget, string, error) {
	var target manualNotebookExpectedTarget
	requestID := r.PathValue("requestId")
	query, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil || !validateManualNotebookRequestID(requestID) {
		return target, requestID, manualNotebookInvalid("Invalid lookup identity")
	}
	fields := map[string]*string{
		"expected_frame_incarnation_id": &target.FrameIncarnationID, "expected_root_frame_id": &target.RootFrameID,
		"expected_root_frame_incarnation_id": &target.RootIncarnationID, "kernel_id": &target.KernelID,
		"language": &target.Language, "environment": &target.Environment,
	}
	for key, values := range query {
		if len(values) != 1 {
			return target, requestID, manualNotebookInvalid("Duplicate lookup parameter")
		}
		if key == "expected_kernel_generation" {
			target.KernelGeneration, err = strconv.ParseUint(values[0], 10, 64)
			if err != nil {
				return target, requestID, manualNotebookInvalid("Invalid kernel generation")
			}
		} else if field, ok := fields[key]; ok {
			*field = values[0]
		} else {
			return target, requestID, manualNotebookInvalid("Unknown lookup parameter")
		}
	}
	return target, requestID, target.validate()
}
