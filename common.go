package main

import (
	ginadapter "github.com/awslabs/aws-lambda-go-api-proxy/gin"
	"github.com/debyltech/go-snipcart-webhook/config"
)

const (
	ValidateUrl string = "https://app.snipcart.com/api/requestvalidation/"
)

var (
	ginLambda     *ginadapter.GinLambda
	webhookConfig *config.Config

	BuildVersion = "development"
)
