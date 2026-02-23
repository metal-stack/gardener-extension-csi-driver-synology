FROM golang:1.26 AS builder

WORKDIR /go/src/github.com/metal-stack/gardener-extension-csi-driver-synology
COPY . .

RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build \
    -ldflags="-s -w" \
    -o /gardener-extension-csi-driver-synology \
    ./cmd/gardener-extension-csi-driver-synology

FROM gcr.io/distroless/static-debian13:nonroot
WORKDIR /
COPY --from=builder /gardener-extension-csi-driver-synology /gardener-extension-csi-driver-synology
USER 65532:65532

ENTRYPOINT ["/gardener-extension-csi-driver-synology"]
