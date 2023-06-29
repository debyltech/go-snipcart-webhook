go-snipcart-webhook:
	CGO_ENABLED=0 go build -tags netgo

go-snipcart-webhook.zip: go-snipcart-webhook
	zip go-snipcart-webhook.zip go-snipcart-webhook

deploy-dev: go-snipcart-webhook.zip
	aws --profile debyltech lambda update-function-code --function-name 'snipcart-webhook-dev' --zip-file 'fileb://go-snipcart-webhook.zip'

deploy-prod: go-snipcart-webhook.zip
	aws --profile debyltech lambda update-function-code --function-name 'webhooks-prod' --zip-file 'fileb://go-snipcart-webhook.zip'

.PHONY: go-snipcart-webhook go-snipcart-webhook.zip