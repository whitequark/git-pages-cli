FROM --platform=$BUILDPLATFORM docker.io/library/golang:1.25-alpine@sha256:26111811bc967321e7b6f852e914d14bede324cd1accb7f81811929a6a57fea9 AS builder
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
ENTRYPOINT ["git-pages-cli"]
