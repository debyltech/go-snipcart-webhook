package main

import (
	"strings"

	"github.com/EasyPost/easypost-go/v4"
	"github.com/debyltech/go-snipcart/snipcart"
)

func IsValidationWhitelisted(address easypost.Address) bool {
	// Romania
	if strings.EqualFold(strings.ToLower(address.State), "if") && strings.EqualFold(strings.ToLower(address.Country), "ro") {
		return true
	}

	// Czechia
	if strings.EqualFold(strings.ToLower(address.Country), "cz") {
		return true
	}

	return false
}

func ValidateAddressFields(shippingAddress snipcart.Address, isProduction bool) *snipcart.ShippingErrors {
	// Ensure the name of the shipping address has at least two words
	shippingAddressName := strings.Split(shippingAddress.Name, " ")

	if len(shippingAddressName) <= 1 {
		return &snipcart.ShippingErrors{
			Errors: []snipcart.ShippingError{
				{
					Key:     "invalid_address_name",
					Message: "Shipping Address name must be at least two words (ex. 'Jon D', 'Jon Doe')",
				},
			},
		}
	}

	// Ensure the first name of the shipping address has more than two characters
	if len(shippingAddressName[0]) <= 2 {
		return &snipcart.ShippingErrors{
			Errors: []snipcart.ShippingError{
				{
					Key:     "invalid_address_firstname_length",
					Message: "Shipping Address first name must be longer than two characters (ex: 'Jon')",
				},
			},
		}
	}

	return nil
}
