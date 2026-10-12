package transcript

import "strings"

// V74 advances the derived projector contract without changing published DDL.
func WebReadModelV74ProjectionStateStatement() string {
	return strings.Replace(WebReadModelV70ProjectionStateStatement(),
		"IN (1,2,3,4,5,6,7,8,9,10,11,12)", "IN (1,2,3,4,5,6,7,8,9,10,11,12,13)", 1)
}

func WebReadModelProjectorV74RebuildStatements() []string {
	statements := WebReadModelProjectorV70RebuildStatements()
	for index, statement := range statements {
		statement = strings.ReplaceAll(statement, "_v69", "_v73")
		if statement == WebReadModelV70ProjectionStateStatement() {
			statement = WebReadModelV74ProjectionStateStatement()
		}
		statements[index] = statement
	}
	return statements
}
