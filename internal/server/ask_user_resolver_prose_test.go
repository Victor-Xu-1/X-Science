package server

import (
	"reflect"
	"testing"

	"synon-go/internal/sciencecapability"
)

func TestAskUserResolverProseCannotAuthorizeExecution(t *testing.T) {
	resolver := sciencecapability.ExecutionEvidenceResolver{EvidenceGroup: "control-point", Skill: "resolver-skill", Implementation: "Resolver Engine"}
	for _, description := range []string{
		"Use supplied input without running Resolver Engine.",
		"无需运行 Resolver Engine，直接使用提供的输入。",
		"Compare the supplied input with Resolver Engine output.",
		"Use the resolver-skill documentation to review the supplied input.",
	} {
		t.Run(description, func(t *testing.T) {
			original := map[string]any{"label": "Use supplied input", "description": description}
			before := string(mustMarshalRawMessage(original))
			options, _ := managedExecutionPrioritizeResolverOption([]any{original}, resolver, resolver.EvidenceGroup, nil, "Resolve the control point?")
			if len(options) != 2 || !reflect.DeepEqual(mapValue(options[1]), original) {
				t.Fatalf("prose was relabeled as executable authority instead of retaining a separate route: %#v", options)
			}
			if string(mustMarshalRawMessage(original)) != before {
				t.Fatal("normalization mutated the model proposal")
			}
			question := map[string]any{"question": "Choose an input source", "options": options}
			selected := compatibilitySelectedAskUserEvidenceResolvers(question, map[string]string{"Choose an input source": "Use supplied input"})
			if len(selected) != 0 {
				t.Fatalf("choosing supplied input authorized a resolver: %#v", selected)
			}
			label := stringValue(mapValue(options[0])["label"])
			selected = compatibilitySelectedAskUserEvidenceResolvers(question, map[string]string{"Choose an input source": label})
			if got := selected["Choose an input source"]; got.Implementation != resolver.Implementation || got.Skill != resolver.Skill || got.EvidenceGroup != resolver.EvidenceGroup {
				t.Fatalf("separate registered route lost its declared identity: %#v", selected)
			}
			again, _ := managedExecutionPrioritizeResolverOption(options, resolver, resolver.EvidenceGroup, nil, "Resolve the control point?")
			if !reflect.DeepEqual(again, options) {
				t.Fatalf("normalization duplicated or changed the registered route: %#v", again)
			}
		})
	}
}
