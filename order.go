package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/debyltech/go-snipcart/snipcart"
)

type OrderCompleteWebhookEvent struct {
	EventName string                 `json:"eventName"`
	CreatedOn time.Time              `json:"createdOn"`
	Order     snipcart.SnipcartOrder `json:"content"`
}

func HandleOrderComplete(body io.ReadCloser) (int, error) {
	var event OrderCompleteWebhookEvent
	if err := json.NewDecoder(body).Decode(&event); err != nil {
		return http.StatusInternalServerError, fmt.Errorf("error with ordercomplete event decode: %s", err.Error())
	}

	return http.StatusOK, nil
}
