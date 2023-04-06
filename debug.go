package main

import "fmt"

func DebugPrintf(s string, a ...any) {
	if webhookConfig.Production {
		fmt.Printf(s, a...)
	}
}

func DebugPrintln(a ...any) {
	if webhookConfig.Production {
		fmt.Println(a...)
	}
}
