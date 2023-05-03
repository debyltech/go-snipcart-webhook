package main

import (
	ginadapter "github.com/awslabs/aws-lambda-go-api-proxy/gin"
	"github.com/debyltech/go-shippr/shippo"
	"github.com/debyltech/go-snipcart-webhook/config"
	"github.com/debyltech/go-snipcart/snipcart"
)

const (
	ValidateUrl string = "https://app.snipcart.com/api/requestvalidation/"
)

var (
	ginLambda      *ginadapter.GinLambda
	webhookConfig  *config.Config
	shippoClient   *shippo.Client
	snipcartClient *snipcart.Client

	BuildVersion = "development"
)

type WebhookEvent struct {
	EventName string `json:"eventName"`
}
