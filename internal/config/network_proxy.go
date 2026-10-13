package config

import "encoding/json"

// MarshalJSON never persists an ambient proxy as an explicit product setting.
// Proxy remains the effective in-memory route for existing runtime consumers;
// its provenance must survive a save/load boundary without being laundered.
func (network NetworkPolicyConfig) MarshalJSON() ([]byte, error) {
	type persistedNetwork NetworkPolicyConfig
	value := persistedNetwork(network)
	if network.ProxyInherited {
		value.Proxy = ""
	}
	return json.Marshal(value)
}
