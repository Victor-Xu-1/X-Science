package server

// The old reserved default assistant key is an actual published input. Normalize
// only that built-in key at presentation; never rewrite a frame or a custom
// delegate/agent name containing a similar substring.
func webCurrentDefaultAssistantName(agentName, delegateName string) string {
	if delegateName == "" && (agentName == "" || agentName == "SYNON") {
		return "X_SCIENCE"
	}
	return agentName
}
