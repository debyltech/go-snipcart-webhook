package main

import (
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
