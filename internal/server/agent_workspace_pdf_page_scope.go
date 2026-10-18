package server

import (
	"context"
	"errors"
)

// Page scope is derived from the same staged bytes used by both PDF readers.
// An unavailable inspector does not invent a bound or block ordinary reading.
type agentWorkspacePDFPageScope struct {
	inspection pdfDocumentInspection
	reason     string
	requested  []int
	valid      []int
	invalid    []int
}

func inspectAgentWorkspacePDFPageScope(ctx context.Context, path string, requested []int) (agentWorkspacePDFPageScope, error) {
	scope := agentWorkspacePDFPageScope{requested: append([]int(nil), requested...)}
	inspection, err := inspectStagedPDFDocument(ctx, path)
	if err != nil {
		if cause := context.Cause(ctx); cause != nil {
			return scope, cause
		}
		var unavailable *pdfDocumentInspectionError
		if !errors.As(err, &unavailable) {
			return scope, err
		}
		scope.reason = unavailable.reason
		scope.valid = append([]int(nil), requested...)
		return scope, nil
	}
	scope.inspection = inspection
	for _, page := range requested {
		if page > inspection.PageCount {
			scope.invalid = append(scope.invalid, page)
		} else {
			scope.valid = append(scope.valid, page)
		}
	}
	return scope, nil
}

func (scope agentWorkspacePDFPageScope) addMetadata(value map[string]any) {
	if scope.reason == "" {
		value["page_count_status"] = "verified"
		value["total_pages"] = scope.inspection.PageCount
		if scope.inspection.Encrypted {
			value["encrypted"] = true
		}
	} else {
		value["page_count_status"] = "unavailable"
		value["page_count_reason"] = scope.reason
	}
	if len(scope.requested) > 0 {
		value["requested_pages"] = scope.requested
		value["pages"] = append([]int{}, scope.valid...)
	}
	if len(scope.invalid) > 0 {
		value["invalid_pages"] = scope.invalid
		value["code"] = "pdf_page_out_of_range"
		if len(scope.valid) > 0 && numberValue(value["visual_parts"]) > 0 {
			value["partial"] = true
			value["message"] = "Only the valid requested PDF pages were read. invalid_pages are outside the verified total_pages; no page was substituted."
		}
	}
}

func (scope agentWorkspacePDFPageScope) rejection(filename, contentType string, size int64) map[string]any {
	if len(scope.requested) == 0 {
		return nil
	}
	code, message := "", ""
	switch {
	case len(scope.invalid) > 0 && len(scope.valid) == 0:
		code, message = "pdf_page_out_of_range", "None of the requested pages exist. Use total_pages to choose an existing page; no visual or text content was read."
	case scope.inspection.Encrypted || scope.reason == "encrypted_pdf":
		code, message = "pdf_encrypted", "The PDF is encrypted. Provide an authorized readable copy before selecting its pages."
	case scope.reason == "invalid_pdf":
		code, message = "pdf_invalid", "The PDF inspector could not parse this document. Provide a valid readable source; this is not an out-of-range page request."
	default:
		return nil
	}
	format, confidence, _ := agentWorkspaceFormatHint(filename, contentType)
	value := map[string]any{
		"filename": filename, "content_type": contentType, "size_bytes": size,
		"ok": false, "executed": false, "preflight": true,
		"message": message, "visual_parts": 0,
		"reader_contract": agentWorkspaceFormatMetadata(format, confidence, "unavailable", filename),
	}
	scope.addMetadata(value)
	value["code"], value["pages"] = code, []int{}
	return value
}
