package main

import _ "embed"

//go:embed diag_ui_v10.js
var diagUIV10 string

func init() {
	uiJS += "\n" + diagUIV10
}
