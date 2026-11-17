FROM --platform=$BUILDPLATFORM docker.io/library/golang:1.27.4-alpine@sha256:70bd7ddea8b6cbe5d6943cde279d6b67feb10cc924fb9100d1d8757c1575b280 AS builder
ARG TARGETOS TARGETARCH
RUN apk --no-cache add ca-certificates git
WORKDIR /build
COPY go.mod go.sum ./
RUN go mod download
COPY *.go ./
RUN GOOS=$TARGETOS GOARCH=$TARGETARCH go build -ldflags "-s -w" .

FROM scratch
COPY --from=builder /etc/ssl/cert.pem /etc/ssl/cert.pem
COPY --from=builder /build/git-pages-cli /bin/git-pages-cli
ENTRYPOINT ["/bin/git-pages-cli"]
