FROM busybox
COPY bin/adapter /
USER 1001:1001
ENTRYPOINT ["/adapter"]
