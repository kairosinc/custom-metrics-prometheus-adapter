REGISTRY?=quay.io/kairosinc
IMAGE?=custom-metrics-prometheus-adapter
TEMP_DIR:=$(shell mktemp -d)
ARCH?=amd64
ALL_ARCH=amd64 arm arm64 ppc64le s390x
ML_PLATFORMS=linux/amd64,linux/arm,linux/arm64,linux/ppc64le,linux/s390x
OUT_DIR?=./_output
VENDOR_DOCKERIZED=1
VERSION?=latest
GOIMAGE=golang:1.10

ifeq ($(ARCH),amd64)
	BASEIMAGE?=busybox
endif
ifeq ($(ARCH),arm)
	BASEIMAGE?=armhf/busybox
endif
ifeq ($(ARCH),arm64)
	BASEIMAGE?=aarch64/busybox
endif
ifeq ($(ARCH),ppc64le)
	BASEIMAGE?=ppc64le/busybox
endif
ifeq ($(ARCH),s390x)
	BASEIMAGE?=s390x/busybox
	GOIMAGE=s390x/golang:1.10
endif

.PHONY: all docker-build push-% push test verify-gofmt gofmt verify build-local-image

all: build

build: vendor
	CGO_ENABLED=0 GOARCH=$(ARCH) go build -a -tags netgo -o $(OUT_DIR)/$(ARCH)/adapter github.com/kairosinc/custom-metrics-prometheus-adapter/cmd/adapter

src_deps=$(shell find pkg cmd -type f -name "*.go")
$(OUT_DIR)/%/adapter: vendor $(src_deps)
	CGO_ENABLED=0 GOARCH=$* go build -tags netgo -o $(OUT_DIR)/$(ARCH)/adapter github.com/kairosinc/custom-metrics-prometheus-adapter/cmd/adapter

docker-build: vendor
	docker run --rm \
		-v $(shell pwd)/bin/:/build \
		-v $(shell pwd):/go/src/github.com/kairosinc/custom-metrics-prometheus-adapter \
		-e GOARCH=$(ARCH) $(GOIMAGE) \
		/bin/bash \
		-c "CGO_ENABLED=0 go build -a -tags netgo -o /build/adapter github.com/kairosinc/custom-metrics-prometheus-adapter/cmd/adapter"
	docker build -t $(REGISTRY)/$(IMAGE):$(VERSION) .

build-local-image: $(OUT_DIR)/$(ARCH)/adapter
	cp deploy/Dockerfile $(TEMP_DIR)
	cp  $(OUT_DIR)/$(ARCH)/adapter $(TEMP_DIR)
	cd $(TEMP_DIR) && sed -i "s|BASEIMAGE|scratch|g" Dockerfile
	docker build -t $(REGISTRY)/$(IMAGE)-$(ARCH):$(VERSION) $(TEMP_DIR)
	rm -rf $(TEMP_DIR)

push-%:
	$(MAKE) ARCH=$* docker-build
	docker push $(REGISTRY)/$(IMAGE)-$*:$(VERSION)

push: ./manifest-tool $(addprefix push-,$(ALL_ARCH))
	./manifest-tool push from-args --platforms $(ML_PLATFORMS) --template $(REGISTRY)/$(IMAGE)-ARCH:$(VERSION) --target $(REGISTRY)/$(IMAGE):$(VERSION)

./manifest-tool:
	curl -sSL https://github.com/estesp/manifest-tool/releases/download/v0.5.0/manifest-tool-linux-amd64 > manifest-tool
	chmod +x manifest-tool

vendor: Gopkg.lock
ifeq ($(VENDOR_DOCKERIZED),1)
	docker run --rm \
		-v $(shell pwd):/go/src/github.com/kairosinc/custom-metrics-prometheus-adapter \
		-w /go/src/github.com/kairosinc/custom-metrics-prometheus-adapter \
		golang:1.10 /bin/bash -c "\
		curl https://raw.githubusercontent.com/golang/dep/master/install.sh | sh \
		&& dep ensure -vendor-only"
else
	dep ensure -vendor-only -v
endif

test: vendor
	CGO_ENABLED=0 go test ./pkg/...

verify-gofmt:
	./hack/gofmt-all.sh -v

gofmt:
	./hack/gofmt-all.sh

verify: verify-gofmt test
