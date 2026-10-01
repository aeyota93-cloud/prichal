# syntax=docker/dockerfile:1
# Причал: one static Go binary plus CA certificates, nothing else.
FROM --platform=$BUILDPLATFORM golang:1.26-alpine AS build
ARG TARGETOS TARGETARCH
RUN apk add --no-cache ca-certificates
WORKDIR /src
COPY go.mod ./
COPY *.go ./
COPY web ./web
COPY scripts ./scripts
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath -ldflags="-s -w" -o /prichal .

FROM scratch
COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --from=build /prichal /prichal
EXPOSE 9443
ENTRYPOINT ["/prichal"]
