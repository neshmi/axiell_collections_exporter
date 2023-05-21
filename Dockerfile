# Build stage
FROM golang:1.19 AS build
ARG TARGETPLATFORM=amd64
WORKDIR /app
COPY . .
RUN CGO_ENABLED=0 GOOS=linux GOARCH=$TARGETPLATFORM go build -o app

# Final stage
FROM scratch
COPY --from=build /app/app /app
ENTRYPOINT ["/app"]