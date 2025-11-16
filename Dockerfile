FROM docker.io/library/golang:1.25-alpine@sha256:aee43c3ccbf24fdffb7295693b6e33b21e01baec1b2a55acc351fde345e9ec34 AS builder
RUN apk --no-cache add ca-certificates git
WORKDIR /build
COPY go.mod go.sum ./
RUN go mod download
COPY *.go ./
RUN go build -ldflags "-s -w" .

FROM scratch
COPY --from=builder /etc/ssl/cert.pem /etc/ssl/cert.pem
COPY --from=builder /build/git-pages-cli /bin/git-pages-cli

ENTRYPOINT ["git-pages-cli"]
