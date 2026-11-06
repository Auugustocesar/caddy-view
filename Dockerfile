# syntax=docker/dockerfile:1

# The UI is built and committed in internal/web/dist, so this stage needs only Go. The driver for SQLite is pure Go,
# which is what lets the binary be static and the final image have nothing in it but the binary.
FROM golang:1.26 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG VERSION=dev
RUN CGO_ENABLED=0 go build -trimpath \
      -ldflags "-s -w -X github.com/Auugustocesar/caddy-view/internal/cli.version=${VERSION}" \
      -o /out/caddy-view ./cmd/caddy-view
# The image has no shell to make the data directory with, so it is made here and copied in with its owner.
RUN mkdir /out/data

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/caddy-view /usr/local/bin/caddy-view
COPY --from=build --chown=65532:65532 /out/data /data
# 65532 is "nonroot": caddy-view does not need root, only to reach the Admin API socket and the files it is given.
USER 65532:65532
VOLUME /data
EXPOSE 8484
# The probe has no login and says nothing but "ok". It calls 127.0.0.1, a name caddy-view always accepts.
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 CMD ["caddy-view", "healthcheck"]
ENTRYPOINT ["caddy-view"]
# Inside a container it has to listen on every interface, and plain HTTP is what it speaks: the compose example publishes
# the port on the loopback of the host only, and TLS is for the Caddy in front of it.
CMD ["serve", "--listen", "0.0.0.0:8484", "--allow-insecure-http", "--data-dir", "/data"]
