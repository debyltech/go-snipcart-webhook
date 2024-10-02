package main

import (
	"strings"

	"github.com/debyltech/go-shippr/shippo"
)

func IsValidationWhitelisted(address shippo.Address) bool {
	if strings.EqualFold(strings.ToLower(address.State), "if") && strings.EqualFold(strings.ToLower(address.Country), "ro") {
		return true
	}

	return false
}
