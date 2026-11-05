package transcript

import "strings"

// V93 widens the derived projector constraint while retaining historical DDL
// and cached row versions. The existing projector work owner rebuilds old views.
func WebReadModelV93ProjectionStateStatement() string {
	return strings.Replace(WebReadModelV79ProjectionStateStatement(),
		"IN (1,2,3,4,5,6,7,8,9,10,11,12,13,14)", "IN (1,2,3,4,5,6,7,8,9,10,11,12,13,14,15)", 1)
}

func WebReadModelProjectorV93RebuildStatements() []string {
	statements := WebReadModelProjectorV79RebuildStatements()
	for index, statement := range statements {
		if statement == WebReadModelV79ProjectionStateStatement() {
			statements[index] = WebReadModelV93ProjectionStateStatement()
			continue
		}
		statements[index] = strings.ReplaceAll(statement, "_v78", "_v92")
	}
	return statements
}
