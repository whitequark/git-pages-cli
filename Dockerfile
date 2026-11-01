FROM --platform=$BUILDPLATFORM docker.io/library/golang:1.26.10-alpine@sha256:5cb67f19c5e7994dcb80c074f7cfca8aeeada45bbdca035f73df269cefdcce61 AS builder
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
