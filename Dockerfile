FROM --platform=$BUILDPLATFORM docker.io/library/golang:1.27.3-alpine@sha256:fa3d596898d9f126af7c01d14ee7597bab3f39cccf392ef36b69fe77cd681f18 AS builder
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
