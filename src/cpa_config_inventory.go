package main

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// CPA v8 moved downstream keys to access.api-keys and upstream keys to
// api-keys.<family>[].keys. Normalize both layouts before listing resources,
// reconciling persisted credential identities, or reading priority.
type cpaConfigInventory struct {
	keys      []string
	resources []cpaConfiguredResource
}

type cpaConfiguredResource struct {
	resource apiResource
	liveID   string
	priority int
}

type cpaModel struct {
	Name  string `yaml:"name"`
	Alias string `yaml:"alias"`
}

type cpaPool struct {
	Enabled         *bool   `yaml:"enabled"`
	IdleConnTimeout *string `yaml:"idle-conn-timeout"`
	MaxIdleConns    *int    `yaml:"max-idle-conns-per-host"`
}

type cpaKeyFields struct {
	APIKey   string            `yaml:"api-key"`
	ProxyURL *string           `yaml:"proxy-url"`
	Prefix   *string           `yaml:"prefix"`
	Priority *int              `yaml:"priority"`
	Headers  map[string]string `yaml:"headers"`
	Models   []cpaModel        `yaml:"models"`
	Pool     *cpaPool          `yaml:"connection-pool"`
}

type cpaProviderGroup struct {
	cpaKeyFields `yaml:",inline"`
	Name         string         `yaml:"name"`
	BaseURL      string         `yaml:"base-url"`
	Disabled     bool           `yaml:"disabled"`
	Keys         []cpaKeyFields `yaml:"keys"`
	LegacyKeys   []cpaKeyFields `yaml:"api-key-entries"`
}

func cpaConfigValue(n *yaml.Node, key string) *yaml.Node {
	for n != nil && n.Kind == yaml.AliasNode {
		n = n.Alias
	}
	if n == nil || n.Kind != yaml.MappingNode {
		return nil
	}
	// Decode the mapping so YAML merge keys and key presence are both respected.
	var values map[string]yaml.Node
	if n.Decode(&values) != nil {
		return nil
	}
	if v, ok := values[key]; ok {
		return &v
	}
	return nil
}

func readCPAConfigInventory(raw string) (cpaConfigInventory, error) {
	// Never expose parser diagnostics: a YAML error can quote a credential.
	var root map[string]yaml.Node
	if err := yaml.Unmarshal([]byte(raw), &root); err != nil {
		return cpaConfigInventory{}, errors.New("CPA config YAML is invalid")
	}
	inv := cpaConfigInventory{}
	upstreams, access := root["api-keys"], root["access"]
	up := &upstreams
	if up.Kind == yaml.AliasNode {
		up = up.Alias
	}
	if client := cpaConfigValue(&access, "api-keys"); client != nil {
		if err := client.Decode(&inv.keys); err != nil {
			return cpaConfigInventory{}, errors.New("CPA access.api-keys is invalid")
		}
	} else if up.Kind == yaml.SequenceNode {
		if err := up.Decode(&inv.keys); err != nil {
			return cpaConfigInventory{}, errors.New("CPA client api-keys is invalid")
		}
	}
	for _, spec := range []struct{ family, legacy, provider string }{
		{"codex", "codex-api-key", "codex"},
		{"xai", "xai-api-key", "xai"},
		{"claude", "claude-api-key", "claude"},
		{"gemini", "gemini-api-key", "gemini"},
		{"interactions", "interactions-api-key", "gemini-interactions"},
		{"vertex", "vertex-api-key", "vertex"},
		{"meta", "meta-api-key", "meta"},
		{"openai-compatibility", "openai-compatibility", ""},
	} {
		legacy := root[spec.legacy]
		node := &legacy
		grouped := false
		if n := cpaConfigValue(up, spec.family); n != nil {
			node, grouped = n, true // presence wins for this family, including an empty list
		}
		var groups []cpaProviderGroup
		if node.Kind != 0 && node.Decode(&groups) != nil {
			return cpaConfigInventory{}, fmt.Errorf("CPA %s API key configuration is invalid", spec.family)
		}
		ordinal := 0
		for i, g := range groups {
			if spec.provider == "" {
				if g.Disabled || strings.TrimSpace(g.BaseURL) == "" {
					continue
				}
				entries := g.LegacyKeys
				if grouped {
					entries = g.Keys
				}
				if len(entries) == 0 {
					inv.addCompatible(g, cpaKeyFields{}, i, 0, true)
				}
				for j, key := range entries {
					inv.addCompatible(g, key, i, j, false)
				}
				continue
			}
			entries := []cpaKeyFields{g.cpaKeyFields}
			if grouped {
				entries = g.Keys
			}
			for _, key := range entries {
				if grouped {
					key = inheritCPAFields(g.cpaKeyFields, key)
				}
				inv.addNative(spec.family, spec.provider, spec.legacy, g.BaseURL, key, ordinal)
				ordinal++
			}
		}
	}
	return inv, nil
}

func cpaString(s *string) string {
	if s == nil {
		return ""
	}
	return strings.TrimSpace(*s)
}

func cpaInt(n *int) int {
	if n == nil {
		return 0
	}
	return *n
}

func inheritCPAFields(group, key cpaKeyFields) cpaKeyFields {
	if key.ProxyURL == nil {
		key.ProxyURL = group.ProxyURL
	}
	if key.Prefix == nil {
		key.Prefix = group.Prefix
	}
	if key.Priority == nil {
		key.Priority = group.Priority
	}
	if key.Headers == nil {
		key.Headers = group.Headers
	}
	if key.Models == nil {
		key.Models = group.Models
	}
	key.Pool = mergeCPAPool(group.Pool, key.Pool, false)
	return key
}

func mergeCPAPool(group, key *cpaPool, inheritBlankTimeout bool) *cpaPool {
	if key == nil {
		return group
	}
	merged := *key
	if group != nil {
		if merged.Enabled == nil {
			merged.Enabled = group.Enabled
		}
		if merged.IdleConnTimeout == nil || (inheritBlankTimeout && cpaString(merged.IdleConnTimeout) == "") {
			merged.IdleConnTimeout = group.IdleConnTimeout
		}
		if merged.MaxIdleConns == nil {
			merged.MaxIdleConns = group.MaxIdleConns
		}
	}
	return &merged
}

func cpaLiveID(kind string, pool *cpaPool, parts ...string) string {
	if pool != nil {
		enabled, max := "", ""
		if pool.Enabled != nil {
			enabled = strconv.FormatBool(*pool.Enabled)
		}
		if pool.MaxIdleConns != nil {
			max = strconv.Itoa(*pool.MaxIdleConns)
		}
		parts = append(parts, "pool:"+enabled+"\x00"+cpaString(pool.IdleConnTimeout)+"\x00"+max)
	}
	return stableID(kind, parts...)
}

func cpaModels(config []cpaModel) []string {
	var models []string
	for _, m := range config {
		if alias := strings.TrimSpace(m.Alias); alias != "" {
			models = append(models, alias)
		} else if name := strings.TrimSpace(m.Name); name != "" {
			models = append(models, name)
		}
	}
	return models
}

func (inv *cpaConfigInventory) addNative(family, provider, legacy, base string, k cpaKeyFields, i int) {
	base, key := strings.TrimSpace(base), strings.TrimSpace(k.APIKey)
	switch family {
	case "codex", "xai", "vertex":
		if base == "" {
			return
		}
	case "meta":
		if key == "" || strings.HasPrefix(key, "dca:") {
			return
		}
		if base == "" {
			base = "https://api.meta.ai/v1"
		}
	default:
		if key == "" && base == "" {
			return
		}
	}
	proxy, prefix := cpaString(k.ProxyURL), normalizePrefixV8(cpaString(k.Prefix))
	headers := make(map[string]string, len(k.Headers))
	for name, value := range k.Headers {
		name, value = strings.TrimSpace(name), strings.TrimSpace(value)
		if name != "" && value != "" {
			headers[name] = value
		}
	}
	models := cpaModels(k.Models)
	name := strings.ToUpper(family) + " API"
	if len(models) > 0 {
		name += " · " + models[0]
	}
	idx := stableAuthIndex(legacy + ":" + base + "+" + key)
	parts := []string{key, base, proxy, prefix, formatSortedHeadersV8(headers)}
	if family == "vertex" {
		parts = parts[:3]
		idx = stableAuthIndex("id:" + stableID("vertex:apikey", parts...))
	}
	inv.addResource(fmt.Sprintf("api:%s:%s:%d", provider, idx, i), provider, name, idx, prefix, base, key, models,
		cpaLiveID(provider+":apikey", k.Pool, parts...), cpaInt(k.Priority))
}

func (inv *cpaConfigInventory) addCompatible(g cpaProviderGroup, k cpaKeyFields, i, j int, keyless bool) {
	name, base := strings.TrimSpace(g.Name), strings.TrimSpace(g.BaseURL)
	provider := openAICompatibleProviderKey(name)
	liveName := strings.ToLower(name)
	if liveName == "" {
		liveName, name = "openai-compatibility", "OpenAI Compatibility"
	}
	key, prefix := strings.TrimSpace(k.APIKey), normalizePrefixV8(cpaString(g.Prefix))
	kind := "openai-compatibility:" + liveName
	parts := []string{key, base, cpaString(k.ProxyURL)}
	if keyless {
		parts = []string{base}
	}
	idx := stableAuthIndex("openai-compatibility:" + base + "+" + key)
	if key == "" {
		idx = stableAuthIndex("id:" + stableID(kind, base))
	}
	inv.addResource(fmt.Sprintf("api:%s:%s:%d:%d", provider, idx, i, j), provider, name, idx, prefix, base, key, cpaModels(g.Models),
		cpaLiveID(kind, mergeCPAPool(g.Pool, k.Pool, true), parts...), cpaInt(g.Priority))
}

func (inv *cpaConfigInventory) addResource(id, provider, name, idx, prefix, base, key string, models []string, liveID string, priority int) {
	inv.resources = append(inv.resources, cpaConfiguredResource{
		resource: apiResource{ID: id, Kind: "API", Provider: provider, DisplayName: name, AuthIndex: idx,
			Prefix: prefix, BaseURL: base, KeyHint: maskKey(key), Status: "active", Models: models, SuggestedModel: suggestedModel(prefix, models)},
		liveID: liveID, priority: priority,
	})
}

func (inv cpaConfigInventory) visible() ([]downstreamKey, []apiResource) {
	keys := make([]downstreamKey, 0, len(inv.keys))
	for _, k := range inv.keys {
		if k = strings.TrimSpace(k); k != "" {
			keys = append(keys, downstreamKey{Fingerprint: fingerprint(k), Hint: maskKey(k)})
		}
	}
	resources := make([]apiResource, 0, len(inv.resources))
	for _, r := range inv.resources {
		resources = append(resources, r.resource)
	}
	return keys, resources
}
