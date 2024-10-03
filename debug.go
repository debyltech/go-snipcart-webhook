package main

import (
	"encoding/json"
	"fmt"
)

func DebugPrintf(s string, a ...any) {
	if !webhookConfig.Production {
		logJson("debug", fmt.Sprintf(s, a...))
	}
}

func DebugPrintln(a ...any) {
	if !webhookConfig.Production {
		fmt.Println(a...)
		logJson("debug", fmt.Sprintln(a...))
	}
}

func DebugPrintMarshalJson(name string, v any) {
	if !webhookConfig.Production {
		jsonDebug, _ := json.Marshal(v)
		fmt.Printf("%s %s\n", name, string(jsonDebug))
	}
}
