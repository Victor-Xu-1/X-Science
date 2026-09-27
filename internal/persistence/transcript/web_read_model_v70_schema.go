package transcript

import "strings"

// Keep historical migration statements immutable. Only the accepted projector
// versions change; existing source and derived rows retain their values.
func WebReadModelV70ProjectionStateStatement() string {
	return strings.Replace(transcriptWebProjectionStateV67Statement,
		"IN (1,2,3,4,5,6,7,8,9,10,11)", "IN (1,2,3,4,5,6,7,8,9,10,11,12)", 1)
}

func WebReadModelProjectorV70RebuildStatements() []string {
	statements := WebReadModelProjectorV67RebuildStatements()
	for index, statement := range statements {
		if statement == transcriptWebProjectionStateV67Statement {
			statements[index] = WebReadModelV70ProjectionStateStatement()
			continue
		}
		statements[index] = strings.ReplaceAll(statement, "_v66", "_v69")
	}
	return statements
}
