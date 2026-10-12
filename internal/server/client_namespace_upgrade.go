package server

import (
	"strings"

	settingsstore "x-science/internal/persistence/settings"
)

var previousWebClientSettingKeys = map[string]string{
	"synonBiomed.session.defaultModelId":  "xScience.session.defaultModelId",
	"synonBiomed.session.subagentModelId": "xScience.session.subagentModelId",
	"synonBiomed.session.effort":          "xScience.session.effort",
	"synonBiomed.sidebar.projectOrder":    "xScience.sidebar.projectOrder",
}

func upgradeWebClientSettingsNamespace(store *settingsstore.Store, userID string) error {
	stored, err := store.List()
	if err != nil {
		return err
	}
	replacements := map[string]string{}
	prefix := webClientSettingsPrefix(userID)
	for key := range stored {
		if !strings.HasPrefix(key, prefix) {
			continue
		}
		if current, found := previousWebClientSettingKeys[strings.TrimPrefix(key, prefix)]; found {
			replacements[key] = prefix + current
		}
	}
	return store.RenameKeys(replacements)
}
