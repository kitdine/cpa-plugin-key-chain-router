package main

import "strings"

func scopedModelV10(model, prefix string) string {
	model = strings.TrimSpace(model)
	prefix = strings.Trim(strings.TrimSpace(prefix), "/")
	if model == "" || prefix == "" {
		return model
	}
	if strings.HasPrefix(model, prefix+"/") {
		return model
	}
	return prefix + "/" + model
}
