# Build stage
FROM --platform=$BUILDPLATFORM golang:1.26 AS build
ARG TARGETOS=linux
ARG TARGETARCH=amd64
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY *.go ./
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath -ldflags="-s -w" -o app

# Final stage
FROM scratch
COPY --from=build /app/app /app
USER 65534:65534
EXPOSE 9037
ENTRYPOINT ["/app"]
