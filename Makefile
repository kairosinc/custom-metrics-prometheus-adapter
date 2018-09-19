.PHONY: build-local build build-push deploy

build-local: vendor
	CGO_ENABLED=0 GOARCH=$(ARCH) go build -a -tags netgo -o build/adapter github.com/kairosinc/custom-metrics-prometheus-adapter/cmd/adapter

build:
	@scripts/build.sh

build-push:
	@scripts/build.sh push

deploy:
	@scripts/deploy.sh