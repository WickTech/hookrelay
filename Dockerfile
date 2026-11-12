FROM golang:1.26-alpine AS builder
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -ldflags="-s -w" -o /hookrelay ./cmd/hookrelay
# bbolt data dir, owned by the nonroot runtime user
RUN mkdir -p /out/data && chown 65532:65532 /out/data

# distroless static: CA certificates for HTTPS targets, no shell
FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=builder /hookrelay /hookrelay
COPY --from=builder --chown=65532:65532 /out/data /data
ENV HOOKRELAY_DB_PATH=/data/hookrelay.db
VOLUME ["/data"]
USER 65532:65532
EXPOSE 8080
ENTRYPOINT ["/hookrelay"]
CMD ["/targets.yaml"]
