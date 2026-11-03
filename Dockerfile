# Module checksums are verified before generation; the runtime contains no source.
FROM golang:1.27.3-bookworm AS builder
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
# gqlgen outputs are committed. Docker must never silently regenerate or tidy
# dependencies: CI is responsible for checking their consistency.
RUN go mod verify
ARG VERSION=dev
RUN CGO_ENABLED=0 GOOS=linux go build -mod=readonly -trimpath \
    -ldflags="-s -w -X main.version=${VERSION}" \
    -o /out/commeng ./cmd/commeng

FROM scratch
COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --from=builder /out/commeng /commeng
USER 65532:65532
EXPOSE 8080
HEALTHCHECK --interval=10s --timeout=5s --start-period=10s --retries=5 CMD ["/commeng", "healthcheck"]
ENTRYPOINT ["/commeng"]
CMD ["serve"]
