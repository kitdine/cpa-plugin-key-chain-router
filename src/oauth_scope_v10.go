package main

import (
	"encoding/json"
	"strings"
)

const methodHostAuthGetV10 = "host.auth.get"

type hostAuthGetResponseV10 struct {
	AuthIndex string          `json:"auth_index"`
	JSON      json.RawMessage `json:"json"`
}

func authPrefixByIndexV10(authIndex string) string {
	authIndex = strings.TrimSpace(authIndex)
	if authIndex == "" { return "" }
	raw, err := callHost(methodHostAuthGetV10, map[string]any{"auth_index": authIndex})
	if err != nil { return "" }
	var resp hostAuthGetResponseV10
	if json.Unmarshal(raw, &resp) != nil || len(resp.JSON) == 0 { return "" }
	var metadata map[string]any
	if json.Unmarshal(resp.JSON, &metadata) != nil { return "" }
	prefix, _ := metadata["prefix"].(string)
	return strings.Trim(strings.TrimSpace(prefix), "/")
}

func resourcesForExecutionV10() []apiResource {
	resources := resourcesWithExactIDsV10()
	for i := range resources {
		if !strings.EqualFold(strings.TrimSpace(resources[i].Kind), "OAuth") || strings.TrimSpace(resources[i].Prefix) != "" { continue }
		resources[i].Prefix = authPrefixByIndexV10(resources[i].AuthIndex)
	}
	return resources
}
