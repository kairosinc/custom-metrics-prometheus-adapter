FROM golang:1.10 as builder

ENV APP=custom-metrics-prometheus-adapter
ENV REPO github.com/kairosinc/$APP
ENV COMM adapter

# dep
RUN \
    curl -O -L https://github.com/golang/dep/releases/download/v0.5.0/dep-linux-amd64 \
    && mv dep-linux-amd64 /usr/bin/dep \
    && chmod +x /usr/bin/dep

COPY . /go/src/$REPO
WORKDIR /go/src/$REPO
RUN dep ensure
RUN go build -a -tags netgo -i -o /go/bin/adapter $REPO/cmd/$COMM

FROM alpine:3.7
COPY --from=builder /go/bin/adapter /adapter
ENTRYPOINT ["./adapter"]