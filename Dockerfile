FROM busybox
COPY adapter /
USER 1001:1001
ENTRYPOINT ["/adapter"]
