package transcript

import "strings"

// V79 widens the derived-version constraint without changing historical DDL
// or relabeling existing cached rows as if they had already been rebuilt.
func WebReadModelV79ProjectionStateStatement() string {
	return strings.Replace(WebReadModelV74ProjectionStateStatement(),
		"IN (1,2,3,4,5,6,7,8,9,10,11,12,13)", "IN (1,2,3,4,5,6,7,8,9,10,11,12,13,14)", 1)
}

func WebReadModelProjectorV79RebuildStatements() []string {
	statements := WebReadModelProjectorV74RebuildStatements()
	for index, statement := range statements {
		if statement == WebReadModelV74ProjectionStateStatement() {
			statements[index] = WebReadModelV79ProjectionStateStatement()
			continue
		}
		statements[index] = strings.ReplaceAll(statement, "_v73", "_v78")
	}
	return statements
}
