package server

import (
	"reflect"
	"testing"
)

func TestSessionRunnerDeliverableIntentDoesNotReverseNegation(t *testing.T) {
	for _, task := range []string{
		"保存设计与评分 CSV。给出简洁 Markdown 研究报告，不额外生成 PDF 或办公报告。",
		"保存 CSV 和 Markdown 报告，不要生成 PDF、DOCX 或 PPTX。",
		"Save the CSV and Markdown report; do not generate PDF, DOCX or PPTX files.",
		"Save CSV and Markdown, but don't create PDF or DOCX reports.",
		"无需导出 PDF，保存 CSV 和 Markdown 报告即可。",
		"Save CSV and Markdown without generating a PDF or a DOCX.",
		"保存 CSV 和 Markdown，不生成 PDF 或 DOCX。",
		"保存 CSV 和 Markdown，不应导出 PDF。",
	} {
		t.Run(task, func(t *testing.T) {
			if missing := missingSessionRunnerRequiredDeliverables(task, []string{"scores.csv", "report.md"}); len(missing) != 0 {
				t.Fatalf("negated output became mandatory: %v", missing)
			}
			if formats := sessionRunnerExplicitDeliverableFormats(task); !reflect.DeepEqual(formats, []string{"csv", "md"}) {
				t.Fatalf("affirmative formats=%v, want CSV and Markdown only", formats)
			}
		})
	}
}

func TestSessionRunnerDeliverableIntentNegatedFilesAreNotRequired(t *testing.T) {
	for _, task := range []string{
		"Save results.csv, but do not create extra.pdf or unused.docx.",
		"保存 results.csv，不要生成 extra.pdf 和 unused.docx。",
		"不要生成 extra.pdf；保存 results.csv。",
	} {
		if names := sessionRunnerExplicitDeliverableNames(task); !reflect.DeepEqual(names, []string{"results.csv"}) {
			t.Errorf("task %q required negated names: %v", task, names)
		}
		if missing := missingSessionRunnerRequiredDeliverables(task, []string{"results.csv"}); len(missing) != 0 {
			t.Errorf("task %q rejected actual required file: %v", task, missing)
		}
	}
}

func TestSessionRunnerDeliverableIntentNegatedPersistenceIsNotMandatory(t *testing.T) {
	for _, task := range []string{
		"Do not save files or reports; explain the result in the final answer.",
		"Don't create downloadable files. Just answer inline.",
		"不需要保存文件或报告，直接回答即可。",
		"无需可下载文件，直接回答。",
	} {
		if sessionRunnerRequiresDurableArtifact(task) {
			t.Errorf("task %q invented mandatory persistence", task)
		}
		if missing := missingSessionRunnerRequiredDeliverables(task, nil); len(missing) != 0 {
			t.Errorf("task %q invented deliverables: %v", task, missing)
		}
	}
}

func TestSessionRunnerDeliverableIntentKeepsAffirmativeRequirements(t *testing.T) {
	for _, task := range []string{
		"Do not alter input.csv, but save results.csv and export a PDF report.",
		"不要修改 input.csv，但必须保存 results.csv 并导出 PDF 报告。",
		"Don't forget to save results.csv and a PDF report.",
	} {
		if missing := missingSessionRunnerRequiredDeliverables(task, []string{"results.csv"}); !reflect.DeepEqual(missing, []string{"a verified .pdf artifact"}) {
			t.Errorf("task %q lost affirmative PDF requirement: %v", task, missing)
		}
		if missing := missingSessionRunnerRequiredDeliverables(task, []string{"results.csv", "report.pdf"}); len(missing) != 0 {
			t.Errorf("task %q rejected complete output: %v", task, missing)
		}
	}
}

func TestSessionRunnerDeliverableIntentDoesNotCaptureLaterInputs(t *testing.T) {
	task := "Save results.csv, then read input.pdf and inspect reference.sdf."
	if names := sessionRunnerExplicitDeliverableNames(task); !reflect.DeepEqual(names, []string{"results.csv"}) {
		t.Fatalf("later inputs became mandatory outputs: %v", names)
	}
	if formats := sessionRunnerExplicitDeliverableFormats(task); !reflect.DeepEqual(formats, []string{"csv"}) {
		t.Fatalf("later input formats became mandatory outputs: %v", formats)
	}
}
