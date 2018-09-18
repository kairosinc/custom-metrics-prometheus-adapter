REGISTRY?=quay.io/kairosinc
IMAGE?=custom-metrics-prometheus-adapter
VERSION?=latest
ARCH?=amd64
ALL_ARCH=amd64 arm arm64 ppc64le s390x

all: build

build: vendor
	CGO_ENABLED=0 GOARCH=$(ARCH) go build -a -tags netgo -o build/adapter github.com/kairosinc/custom-metrics-prometheus-adapter/cmd/adapter

docker-build: vendor
	docker build -t $(REGISTRY)/$(IMAGE):$(VERSION) .