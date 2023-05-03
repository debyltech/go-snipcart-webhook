package main

import (
	"io"

	"github.com/debyltech/go-snipcart/snipcart"
)

func HandleTaxCalculation(body io.ReadCloser) (*snipcart.SnipcartWebhookTaxResponse, error) {
	taxes := snipcart.SnipcartWebhookTaxResponse{
		Taxes: []snipcart.SnipcartTax{
			{
				Name:             "New Hampshire (company does not meet threshold for sales tax in your state)",
				Amount:           0.00,
				NumberForInvoice: "TAX-000",
				Rate:             0.0,
			},
		},
	}

	return &taxes, nil
}
