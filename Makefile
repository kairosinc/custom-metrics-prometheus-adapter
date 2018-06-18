REGISTRY?=quay.io/kairosinc
IMAGE?=custom-metrics-prometheus-adapter
TEMP_DIR:=$(shell mktemp -d)
ARCH?=amd64
ALL_ARCH=amd64 arm arm64 ppc64le s390x
ML_PLATFORMS=linux/amd64,linux/arm,linux/arm64,linux/ppc64le,linux/s390x
OUT_DIR?=./_output
VENDOR_DOCKERIZED=1
VERSION?=latest
GOIMAGE=golang:1.8

.PHONY: all build docker-build push-% push test verify-gofmt gofmt verify

all: build

build: vendor
	CGO_ENABLED=0 GOARCH=$(ARCH) go build -a -tags netgo -o $(OUT_DIR)/$(ARCH)/adapter github.com/kairosinc/custom-metrics-prometheus-adapter/cmd/adapter

docker-build: vendor
	docker run -it \
		-v $(shell pwd)/bin/:/build \
		-v $(shell pwd):/go/src/github.com/kairosinc/custom-metrics-prometheus-adapter \
		-e GOARCH=$(ARCH) $(GOIMAGE) \
		/bin/bash \
		-c "CGO_ENABLED=0 go build -a -tags netgo -o /build/adapter github.com/kairosinc/custom-metrics-prometheus-adapter/cmd/adapter"
	docker build -t $(REGISTRY)/$(IMAGE):$(VERSION)

push-%:
	$(MAKE) ARCH=$* docker-build
	docker push $(REGISTRY)/$(IMAGE)-$*:$(VERSION)

push: ./manifest-tool $(addprefix push-,$(ALL_ARCH))
	./manifest-tool push from-args --platforms $(ML_PLATFORMS) --template $(REGISTRY)/$(IMAGE)-ARCH:$(VERSION) --target $(REGISTRY)/$(IMAGE):$(VERSION)

./manifest-tool:
	curl -sSL https://github.com/estesp/manifest-tool/releases/download/v0.5.0/manifest-tool-linux-amd64 > manifest-tool
	chmod +x manifest-tool

vendor: glide.lock
ifeq ($(VENDOR_DOCKERIZED),1)
	docker run -it -v $(shell pwd):/go/src/github.com/directxman12/k8s-prometheus-adapter -w /go/src/github.com/directxman12/k8s-prometheus-adapter golang:1.8 /bin/bash -c "\
		curl https://glide.sh/get | sh \
		&& glide install -v"
else
	glide install -v
endif

test: vendor
	CGO_ENABLED=0 go test ./pkg/...

verify-gofmt:
	./hack/gofmt-all.sh -v

gofmt:
	./hack/gofmt-all.sh

verify: verify-gofmt test
