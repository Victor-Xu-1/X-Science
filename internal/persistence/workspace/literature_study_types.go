package workspace

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"regexp"
	"strings"
	"unicode/utf8"
)

const LiteratureStudySchema = "x-science.literature-study.v1"
const LiteratureStudyMaxOutputBytes = 24_000
const literatureStudyMaxRevision = 9_007_199_254_740_991

var ErrLiteratureStudySourceChanged = errors.New("literature study source changed")
var ErrLiteratureStudyPublicationConflict = errors.New("literature study publication changed")
var studyLanguage = regexp.MustCompile(`^[A-Za-z0-9]+(?:-[A-Za-z0-9]+)*$`)

type LiteratureStudySections struct {
	Question    *string `json:"question"`
	Population  *string `json:"population"`
	Methods     *string `json:"methods"`
	Results     *string `json:"results"`
	Limitations *string `json:"limitations"`
}
type LiteratureStudySnapshot struct {
	SourceKey           string                  `json:"source_key"`
	Coverage            string                  `json:"coverage"`
	AttachmentVersionID string                  `json:"attachment_version_id,omitempty"`
	Model               string                  `json:"model"`
	Sections            LiteratureStudySections `json:"sections"`
}
type LiteratureStudyView struct {
	Schema    string                   `json:"schema"`
	SourceKey string                   `json:"source_key"`
	Revision  int64                    `json:"revision"`
	Available bool                     `json:"available"`
	Snapshot  *LiteratureStudySnapshot `json:"snapshot,omitempty"`
}
type LiteratureStudyGenerate struct {
	OperationID       string `json:"operation_id"`
	Language          string `json:"language"`
	ExpectedSourceKey string `json:"expected_source_key"`
	ExpectedRevision  int64  `json:"expected_revision"`
}

func ValidLiteratureStudyLanguage(value string) bool {
	return len(value) > 0 && len(value) <= 64 && studyLanguage.MatchString(value)
}
func (input LiteratureStudyGenerate) valid() bool {
	return validLiteratureID(input.OperationID) && ValidLiteratureStudyLanguage(input.Language) && len(input.ExpectedSourceKey) > 0 && len(input.ExpectedSourceKey) <= 4096 && input.ExpectedRevision >= 0 && input.ExpectedRevision < literatureStudyMaxRevision
}

// Missing fields are not equivalent to an explicitly reported null.
func DecodeLiteratureStudySections(raw []byte) (LiteratureStudySections, error) {
	var sections LiteratureStudySections
	if len(raw) == 0 || len(raw) > LiteratureStudyMaxOutputBytes {
		return sections, ErrLiteratureInvalid
	}
	tokenReader := json.NewDecoder(bytes.NewReader(raw))
	token, err := tokenReader.Token()
	if err != nil || token != json.Delim('{') {
		return sections, ErrLiteratureInvalid
	}
	keys := map[string]bool{}
	for tokenReader.More() {
		token, err := tokenReader.Token()
		key, ok := token.(string)
		if err != nil || !ok || keys[key] {
			return sections, ErrLiteratureInvalid
		}
		keys[key] = true
		var value json.RawMessage
		if tokenReader.Decode(&value) != nil {
			return sections, ErrLiteratureInvalid
		}
	}
	if _, err := tokenReader.Token(); err != nil || len(keys) != 5 {
		return sections, ErrLiteratureInvalid
	}
	for _, key := range []string{"question", "population", "methods", "results", "limitations"} {
		if !keys[key] {
			return sections, ErrLiteratureInvalid
		}
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var trailing any
	if decoder.Decode(&sections) != nil || decoder.Decode(&trailing) != io.EOF {
		return sections, ErrLiteratureInvalid
	}
	if !validStudySections(sections) {
		return sections, ErrLiteratureInvalid
	}
	return sections, nil
}
func validStudySections(s LiteratureStudySections) bool {
	for _, value := range []*string{s.Question, s.Population, s.Methods, s.Results, s.Limitations} {
		if value != nil && (strings.TrimSpace(*value) == "" || !utf8.ValidString(*value) || utf8.RuneCountInString(*value) > 2400) {
			return false
		}
	}
	return true
}
func validStudySnapshot(s LiteratureStudySnapshot) bool {
	if len(s.SourceKey) == 0 || len(s.SourceKey) > 4096 || strings.TrimSpace(s.Model) == "" || len(s.Model) > 512 || !validStudySections(s.Sections) {
		return false
	}
	return s.Coverage == "abstract" && s.AttachmentVersionID == "" || (s.Coverage == "full-text" || s.Coverage == "passages") && s.AttachmentVersionID != ""
}
func decodeStudySnapshot(raw string) (LiteratureStudySnapshot, error) {
	var snapshot LiteratureStudySnapshot
	if len(raw) > 40_000 {
		return snapshot, ErrLiteratureInvalid
	}
	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.DisallowUnknownFields()
	var extra any
	if decoder.Decode(&snapshot) != nil || decoder.Decode(&extra) != io.EOF || !validStudySnapshot(snapshot) {
		return snapshot, errors.New("invalid persisted literature study snapshot")
	}
	var envelope map[string]json.RawMessage
	if json.Unmarshal([]byte(raw), &envelope) != nil {
		return snapshot, ErrLiteratureInvalid
	}
	if _, err := DecodeLiteratureStudySections(envelope["sections"]); err != nil {
		return snapshot, err
	}
	return snapshot, nil
}
