package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

const methodHostAuthSaveV11 = "host.auth.save"

var managedPriorityMuV11 sync.Mutex

// ensureCandidateCPAPriorityV11 fixes the one CPA selector constraint KCR cannot
// override from scheduler.pick: plugins only receive the highest currently
// eligible credential-priority tier. We persistently raise a KCR-selected
// credential to the provider's already-existing maximum priority before issuing
// the nested execution. This is deliberately not a request-scoped temporary
// priority override: concurrent requests therefore never observe a transient
// priority value that must later be restored.
//
// Priority is not part of CPA's config API StableID, so this does not change the
// candidate's live Auth.ID. Existing exact ticket identity remains authoritative.
func ensureCandidateCPAPriorityV11(c *PolicyCandidate) (bool, error) {
	if c == nil || strings.TrimSpace(c.Provider) == "" {
		return false, nil
	}

	managedPriorityMuV11.Lock()
	defer managedPriorityMuV11.Unlock()

	target, targetOK := providerMaxCPAPriorityV11(c.Provider)
	if !targetOK {
		return false, fmt.Errorf("kcr managed priority: cannot determine CPA priority tier for provider %q", c.Provider)
	}
	current, currentOK := candidateCurrentCPAPriorityV11(c)
	if !currentOK {
		return false, fmt.Errorf("kcr managed priority: cannot determine CPA priority for candidate %q", c.Name)
	}
	if current >= target {
		return false, nil
	}

	resources := resourcesWithExactIDsV10()
	resource := exactResourceForCandidateV10(c, resources)
	kind := strings.TrimSpace(c.ResourceKind)
	if resource != nil && strings.TrimSpace(resource.Kind) != "" {
		kind = strings.TrimSpace(resource.Kind)
	}

	var err error
	if strings.EqualFold(kind, "API") {
		err = persistConfigPriorityV11(c, target)
		if err == nil {
			// CPA debounces config reload by 150ms. Give the watcher enough time to
			// rebuild its AuthManager before this request enters host.model.execute.
			time.Sleep(400 * time.Millisecond)
		}
	} else {
		err = persistOAuthPriorityV11(c, target)
	}
	if err != nil {
		return false, fmt.Errorf("kcr managed priority: %w", err)
	}

	persisted, ok := candidateCurrentCPAPriorityV11(c)
	if !ok || persisted < target {
		return false, fmt.Errorf("kcr managed priority: priority update for %q did not persist (want %d, got %d)", c.Name, target, persisted)
	}
	return true, nil
}

func providerMaxCPAPriorityV11(provider string) (int, bool) {
	provider = strings.TrimSpace(provider)
	if provider == "" {
		return 0, false
	}
	resources := resourcesWithExactIDsV10()
	known := false
	maxPriority := 0
	for i := range resources {
		r := &resources[i]
		if !strings.EqualFold(strings.TrimSpace(r.Provider), provider) {
			continue
		}
		probe := &PolicyCandidate{
			ResourceID: r.ID, ResourceKind: r.Kind, Provider: r.Provider,
			AuthID: r.AuthID, AuthIndex: r.AuthIndex,
		}
		priority, ok := candidateCurrentCPAPriorityV11(probe)
		if !ok {
			continue
		}
		if !known || priority > maxPriority {
			maxPriority = priority
			known = true
		}
	}
	return maxPriority, known
}

func candidateCurrentCPAPriorityV11(c *PolicyCandidate) (int, bool) {
	if c == nil {
		return 0, false
	}
	if d := candidateCPADiagnosticV10(c); d.PriorityKnown {
		return d.Priority, true
	}
	return 0, false
}

type hostAuthPhysicalV11 struct {
	AuthIndex string          `json:"auth_index"`
	Name      string          `json:"name,omitempty"`
	Path      string          `json:"path,omitempty"`
	JSON      json.RawMessage `json:"json"`
}

func persistOAuthPriorityV11(c *PolicyCandidate, target int) error {
	idx := strings.TrimSpace(c.AuthIndex)
	if idx == "" {
		return fmt.Errorf("OAuth candidate %q has no AuthIndex", c.Name)
	}
	raw, err := callHost(methodHostAuthGetV10, map[string]any{"auth_index": idx})
	if err != nil {
		return fmt.Errorf("read OAuth auth file %s: %w", idx, err)
	}
	var resp hostAuthPhysicalV11
	if err := json.Unmarshal(raw, &resp); err != nil {
		return fmt.Errorf("decode OAuth auth file %s: %w", idx, err)
	}
	if len(bytes.TrimSpace(resp.JSON)) == 0 {
		return fmt.Errorf("OAuth auth file %s is empty", idx)
	}
	var doc map[string]any
	if err := json.Unmarshal(resp.JSON, &doc); err != nil {
		return fmt.Errorf("decode OAuth auth JSON %s: %w", idx, err)
	}
	doc["priority"] = target
	updated, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return err
	}
	updated = append(updated, '\n')
	name := strings.TrimSpace(resp.Name)
	if !strings.HasSuffix(strings.ToLower(name), ".json") {
		name = filepath.Base(strings.TrimSpace(resp.Path))
	}
	if name == "" || !strings.HasSuffix(strings.ToLower(name), ".json") {
		return fmt.Errorf("OAuth auth file name unavailable for %s", idx)
	}
	_, err = callHost(methodHostAuthSaveV11, map[string]any{"name": name, "json": json.RawMessage(updated)})
	if err != nil {
		return fmt.Errorf("save OAuth priority %s: %w", idx, err)
	}
	return nil
}

type configPriorityLocationV11 struct {
	Section string
	Outer   int
}

func persistConfigPriorityV11(c *PolicyCandidate, target int) error {
	runtimeState.RLock()
	path := strings.TrimSpace(runtimeState.configPath)
	runtimeState.RUnlock()
	if path == "" {
		return fmt.Errorf("CPA config path is unavailable")
	}
	for attempt := 0; attempt < 3; attempt++ {
		before, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("read CPA config: %w", err)
		}
		loc, err := configPriorityLocationV11ForCandidate(string(before), c)
		if err != nil {
			return err
		}
		patched, changed, err := patchTopListItemPriorityV11(string(before), loc, target)
		if err != nil {
			return err
		}
		if !changed {
			return nil
		}
		latest, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if !bytes.Equal(before, latest) {
			continue
		}
		info, err := os.Stat(path)
		if err != nil {
			return err
		}
		tmp, err := os.CreateTemp(filepath.Dir(path), ".kcr-priority-*.yaml")
		if err != nil {
			return err
		}
		tmpName := tmp.Name()
		ok := false
		defer func() {
			if !ok {
				_ = os.Remove(tmpName)
			}
		}()
		if err = tmp.Chmod(info.Mode().Perm()); err == nil {
			_, err = tmp.WriteString(patched)
		}
		if err == nil {
			err = tmp.Sync()
		}
		if closeErr := tmp.Close(); err == nil {
			err = closeErr
		}
		if err != nil {
			return err
		}
		latest, err = os.ReadFile(path)
		if err != nil {
			return err
		}
		if !bytes.Equal(before, latest) {
			_ = os.Remove(tmpName)
			continue
		}
		if err = os.Rename(tmpName, path); err != nil {
			return err
		}
		ok = true
		return nil
	}
	return fmt.Errorf("CPA config changed concurrently while updating priority")
}

func configPriorityLocationV11ForCandidate(raw string, c *PolicyCandidate) (configPriorityLocationV11, error) {
	provider := strings.TrimSpace(c.Provider)
	idx := strings.TrimSpace(c.AuthIndex)
	if provider == "" || idx == "" {
		return configPriorityLocationV11{}, fmt.Errorf("candidate %q lacks provider/AuthIndex", c.Name)
	}
	lines := preprocessYAMLLines(raw)
	type spec struct {
		section, provider, seed string
		vertex                  bool
	}
	for _, sp := range []spec{
		{"codex-api-key", "codex", "codex-api-key", false},
		{"xai-api-key", "xai", "xai-api-key", false},
		{"claude-api-key", "claude", "claude-api-key", false},
		{"gemini-api-key", "gemini", "gemini-api-key", false},
		{"interactions-api-key", "gemini-interactions", "interactions-api-key", false},
		{"vertex-api-key", "vertex", "vertex", true},
	} {
		if !strings.EqualFold(provider, sp.provider) {
			continue
		}
		matched := -1
		for i, e := range parseTopMapList(lines, sp.section) {
			key := scalar(e.Fields["api-key"])
			base := scalar(e.Fields["base-url"])
			proxyURL := scalar(e.Fields["proxy-url"])
			entryIdx := ""
			if sp.vertex {
				entryIdx = stableAuthIndex("id:" + stableID("vertex:apikey", key, base, proxyURL))
			} else {
				entryIdx = stableAuthIndex(sp.seed + ":" + base + "+" + key)
			}
			if entryIdx == idx {
				if matched >= 0 {
					return configPriorityLocationV11{}, fmt.Errorf("config credential %s is ambiguous", idx)
				}
				matched = i
			}
		}
		if matched >= 0 {
			return configPriorityLocationV11{Section: sp.section, Outer: matched}, nil
		}
	}

	matchedOuter := -1
	for i, e := range parseTopMapList(lines, "openai-compatibility") {
		if parseBool(scalar(e.Fields["disabled"]), false) || !strings.EqualFold(openAICompatibleProviderKey(scalar(e.Fields["name"])), provider) {
			continue
		}
		base := scalar(e.Fields["base-url"])
		entries := e.Nested["api-key-entries"]
		if len(entries) == 0 {
			entries = []yamlMapEntry{{Fields: map[string]string{}}}
		}
		for _, kent := range entries {
			key := scalar(kent.Fields["api-key"])
			entryIdx := ""
			if key != "" {
				entryIdx = stableAuthIndex("openai-compatibility:" + base + "+" + key)
			} else {
				name := strings.ToLower(strings.TrimSpace(scalar(e.Fields["name"])))
				if name == "" {
					name = "openai-compatibility"
				}
				entryIdx = stableAuthIndex("id:" + stableID("openai-compatibility:"+name, base))
			}
			if entryIdx == idx {
				if matchedOuter >= 0 && matchedOuter != i {
					return configPriorityLocationV11{}, fmt.Errorf("OpenAI-compatible credential %s is ambiguous", idx)
				}
				matchedOuter = i
			}
		}
	}
	if matchedOuter >= 0 {
		return configPriorityLocationV11{Section: "openai-compatibility", Outer: matchedOuter}, nil
	}
	return configPriorityLocationV11{}, fmt.Errorf("config credential %s (%s) was not found", idx, provider)
}

func patchTopListItemPriorityV11(raw string, loc configPriorityLocationV11, target int) (string, bool, error) {
	newline := "\n"
	if strings.Contains(raw, "\r\n") {
		newline = "\r\n"
		raw = strings.ReplaceAll(raw, "\r\n", "\n")
	}
	lines := strings.Split(raw, "\n")
	sectionLine := -1
	for i, line := range lines {
		if leadingSpacesV11(line) == 0 && strings.TrimSpace(stripYAMLComment(line)) == loc.Section+":" {
			sectionLine = i
			break
		}
	}
	if sectionLine < 0 {
		return "", false, fmt.Errorf("CPA config section %q not found", loc.Section)
	}
	sectionEnd := len(lines)
	for i := sectionLine + 1; i < len(lines); i++ {
		clean := strings.TrimSpace(stripYAMLComment(lines[i]))
		if clean != "" && leadingSpacesV11(lines[i]) == 0 {
			sectionEnd = i
			break
		}
	}
	itemStart := -1
	itemEnd := sectionEnd
	ordinal := -1
	for i := sectionLine + 1; i < sectionEnd; i++ {
		clean := strings.TrimSpace(stripYAMLComment(lines[i]))
		if leadingSpacesV11(lines[i]) == 2 && strings.HasPrefix(clean, "-") {
			ordinal++
			if ordinal == loc.Outer {
				itemStart = i
				continue
			}
			if itemStart >= 0 {
				itemEnd = i
				break
			}
		}
	}
	if itemStart < 0 {
		return "", false, fmt.Errorf("CPA config item %s[%d] not found", loc.Section, loc.Outer)
	}
	for i := itemStart + 1; i < itemEnd; i++ {
		if leadingSpacesV11(lines[i]) != 4 {
			continue
		}
		key, value, ok := splitYAMLKeyValue(strings.TrimSpace(stripYAMLComment(lines[i])))
		if !ok || key != "priority" {
			continue
		}
		if n, err := strconv.Atoi(strings.TrimSpace(scalar(value))); err == nil && n == target {
			return strings.Join(lines, newline), false, nil
		}
		lines[i] = "    priority: " + strconv.Itoa(target)
		return strings.Join(lines, newline), true, nil
	}
	insert := "    priority: " + strconv.Itoa(target)
	lines = append(lines, "")
	copy(lines[itemStart+2:], lines[itemStart+1:])
	lines[itemStart+1] = insert
	return strings.Join(lines, newline), true, nil
}

func leadingSpacesV11(s string) int {
	n := 0
	for n < len(s) && s[n] == ' ' {
		n++
	}
	return n
}
