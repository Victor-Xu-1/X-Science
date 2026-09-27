package server

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	workspace "synon-go/internal/persistence/workspace"
)

func TestWebDocumentConversionUsesNativeParsers(t *testing.T) {
	root := t.TempDir()
	files := writeWebDocumentFixtures(t, filepath.Join(root, "project"))

	docx, err := convertWebDOCXToMarkdown(files["docx"])
	if err != nil || docx != "# Title\n\nBody text" {
		t.Fatalf("DOCX markdown = %q err=%v", docx, err)
	}
	csv, err := convertWebSpreadsheetToJSON(files["csv"])
	if err != nil || len(csv.Sheets) != 1 || len(csv.Sheets[0].Data) != 2 ||
		csv.Sheets[0].Data[1][0] != "alpha" {
		t.Fatalf("CSV workbook = %#v err=%v", csv, err)
	}
	xlsx, err := convertWebSpreadsheetToJSON(files["xlsx"])
	if err != nil {
		t.Fatal(err)
	}
	if len(xlsx.Sheets) != 1 || xlsx.Sheets[0].Name != "Data" ||
		len(xlsx.Sheets[0].Data) != 2 || xlsx.Sheets[0].Data[0][0] != "name" ||
		xlsx.Sheets[0].Data[0][1] != float64(7) ||
		xlsx.Sheets[0].Data[1][0] != "alpha" || xlsx.Sheets[0].Data[1][1] != true ||
		len(xlsx.Sheets[0].Merges) != 1 ||
		xlsx.Sheets[0].Merges[0].End != (webExcelCellRef{Row: 0, Col: 1}) {
		t.Fatalf("XLSX workbook = %#v", xlsx)
	}
	pptx, err := convertWebPPTXToJSON(files["pptx"])
	if err != nil || len(pptx.Slides) != 2 ||
		pptx.Slides[0].SlideNumber != 1 || pptx.Slides[1].SlideNumber != 2 {
		t.Fatalf("PPTX = %#v err=%v", pptx, err)
	}
	first := pptx.Slides[0].Content.(map[string]any)
	if first["text"] != "First" {
		t.Fatalf("first slide = %#v", first)
	}
}

func TestWebDocumentConversionAPIContract(t *testing.T) {
	stateRoot := t.TempDir()
	projectRoot := filepath.Join(stateRoot, "project")
	files := writeWebDocumentFixtures(t, projectRoot)
	srv := newWebFSProjectTestServer(t, stateRoot, projectRoot, "local")
	handler := srv.Handler()
	tests := []struct {
		name string
		file string
		to   string
	}{
		{"markdown", files["markdown"], "markdown"},
		{"docx", files["docx"], "markdown"},
		{"csv", files["csv"], "excel-json"},
		{"xlsx", files["xlsx"], "excel-json"},
		{"pptx", files["pptx"], "ppt-json"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			response := webFSTestRequest(t, handler, http.MethodPost,
				"/api/document/convert", "local", map[string]any{
					"file_path": test.file, "workspace": projectRoot, "to": test.to,
				})
			requireWebFSStatus(t, response, http.StatusOK)
			var result struct {
				To     string                      `json:"to"`
				Result webDocumentConversionResult `json:"result"`
			}
			decodeWebFSTestResponse(t, response, &result)
			if result.To != test.to || !result.Result.Success || result.Result.Data == nil {
				t.Fatalf("conversion result = %#v", result)
			}
		})
	}

	unsupported := webFSTestRequest(t, handler, http.MethodPost,
		"/api/document/convert", "local", map[string]any{
			"file_path": files["markdown"], "workspace": projectRoot, "to": "ppt-json",
		})
	requireWebFSStatus(t, unsupported, http.StatusOK)
	var failed struct {
		Result webDocumentConversionResult `json:"result"`
	}
	decodeWebFSTestResponse(t, unsupported, &failed)
	if failed.Result.Success || failed.Result.Error == "" {
		t.Fatalf("unsupported conversion = %#v", failed)
	}

	invalid := webFSTestRequest(t, handler, http.MethodPost,
		"/api/document/convert", "local", map[string]any{
			"file_path": files["markdown"], "to": "html",
		})
	requireWebFSStatus(t, invalid, http.StatusBadRequest)
}

func TestWebDocumentConversionAPIConvertsOwnedArtifactVersion(t *testing.T) {
	stateRoot := t.TempDir()
	projectRoot := filepath.Join(stateRoot, "project")
	files := writeWebDocumentFixtures(t, projectRoot)
	srv := newWebFSProjectTestServer(t, stateRoot, projectRoot, "local")

	source, err := os.Open(files["docx"])
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	_, version, err := srv.workspaceStore.SaveArtifactVersionFromReader(
		context.Background(),
		workspace.SaveArtifactVersionReaderInput{
			ArtifactID: "document-artifact", ProjectID: "web-fs-project", Name: "report.docx",
			Kind: "application/vnd.openxmlformats-officedocument.wordprocessingml.document", Content: source,
		},
	)
	if err != nil {
		t.Fatal(err)
	}

	response := webFSTestRequest(t, srv.Handler(), http.MethodPost,
		"/api/document/convert", "local", map[string]any{
			"artifact_id": "document-artifact", "version_id": version.ID, "to": "markdown",
		})
	requireWebFSStatus(t, response, http.StatusOK)
	var result struct {
		To     string                      `json:"to"`
		Result webDocumentConversionResult `json:"result"`
	}
	decodeWebFSTestResponse(t, response, &result)
	if result.To != "markdown" || !result.Result.Success || result.Result.Data != "# Title\n\nBody text" {
		t.Fatalf("artifact conversion result = %#v", result)
	}

	conflict := webFSTestRequest(t, srv.Handler(), http.MethodPost,
		"/api/document/convert", "local", map[string]any{
			"artifact_id": "document-artifact", "file_path": files["docx"], "to": "markdown",
		})
	requireWebFSStatus(t, conflict, http.StatusBadRequest)

	missing := webFSTestRequest(t, srv.Handler(), http.MethodPost,
		"/api/document/convert", "local", map[string]any{
			"artifact_id": "missing-artifact", "to": "markdown",
		})
	requireWebFSStatus(t, missing, http.StatusNotFound)

	orphanVersion := webFSTestRequest(t, srv.Handler(), http.MethodPost,
		"/api/document/convert", "local", map[string]any{
			"version_id": version.ID, "to": "markdown",
		})
	requireWebFSStatus(t, orphanVersion, http.StatusBadRequest)
}

func TestWebDocumentConversionAPIPreservesSparseWorkbookRows(t *testing.T) {
	stateRoot := t.TempDir()
	projectRoot := filepath.Join(stateRoot, "project")
	filePath := filepath.Join(projectRoot, "sparse-workbook.xlsx")
	writeWebDocumentZip(t, filePath, map[string]string{
		"xl/workbook.xml":            `<workbook xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships"><sheets><sheet name="Sparse" r:id="rId1"/><sheet name="Formula" r:id="rId2"/></sheets></workbook>`,
		"xl/_rels/workbook.xml.rels": `<Relationships><Relationship Id="rId1" Target="worksheets/sheet1.xml"/><Relationship Id="rId2" Target="worksheets/sheet2.xml"/></Relationships>`,
		"xl/worksheets/sheet1.xml":   `<worksheet><sheetData><row r="1"><c r="A1" t="inlineStr"><is><t>Header</t></is></c></row><row r="3"><c r="A3" t="inlineStr"><is><t>After blank row</t></is></c></row></sheetData></worksheet>`,
		"xl/worksheets/sheet2.xml":   `<worksheet><sheetData><row r="1"><c r="A1"><v>2</v></c><c r="B1"><v>3</v></c><c r="C1"><f>SUM(A1:B1)</f><v>5</v></c></row></sheetData></worksheet>`,
	})
	srv := newWebFSProjectTestServer(t, stateRoot, projectRoot, "local")
	source, err := os.Open(filePath)
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	_, version, err := srv.workspaceStore.SaveArtifactVersionFromReader(context.Background(), workspace.SaveArtifactVersionReaderInput{
		ArtifactID: "sparse-workbook", ProjectID: "web-fs-project", Name: "sparse-workbook.xlsx",
		Kind: "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet", Content: source,
	})
	if err != nil {
		t.Fatal(err)
	}
	handler := srv.Handler()
	for _, test := range []struct {
		name  string
		input map[string]any
	}{
		{"workspace file", map[string]any{"file_path": filePath, "workspace": projectRoot, "to": "excel-json"}},
		{"owned artifact version", map[string]any{"artifact_id": "sparse-workbook", "version_id": version.ID, "to": "excel-json"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			response := webFSTestRequest(t, handler, http.MethodPost, "/api/document/convert", "local", test.input)
			requireWebFSStatus(t, response, http.StatusOK)
			var result struct {
				Result struct {
					Success bool             `json:"success"`
					Data    webExcelWorkbook `json:"data"`
				} `json:"result"`
			}
			decodeWebFSTestResponse(t, response, &result)
			sheets := result.Result.Data.Sheets
			if !result.Result.Success || len(sheets) != 2 || sheets[0].Name != "Sparse" || sheets[1].Name != "Formula" {
				t.Fatalf("workbook conversion = %s", response.Body.String())
			}
			rows := sheets[0].Data
			if len(rows) != 3 || len(rows[0]) != 1 || rows[0][0] != "Header" ||
				rows[1] == nil || len(rows[1]) != 0 || len(rows[2]) != 1 || rows[2][0] != "After blank row" {
				t.Fatalf("sparse rows must preserve their positions with [] for a blank row: %s", response.Body.String())
			}
			formulaRows := sheets[1].Data
			// Preview reads the workbook's cached value; it never executes formulas.
			if len(formulaRows) != 1 || len(formulaRows[0]) != 3 || formulaRows[0][0] != float64(2) ||
				formulaRows[0][1] != float64(3) || formulaRows[0][2] != float64(5) {
				t.Fatalf("formula sheet = %#v", formulaRows)
			}
		})
	}
	foreign := webFSTestRequest(t, handler, http.MethodPost, "/api/document/convert", "foreign-user", map[string]any{
		"artifact_id": "sparse-workbook", "version_id": version.ID, "to": "excel-json",
	})
	requireWebFSStatus(t, foreign, http.StatusNotFound)
}
