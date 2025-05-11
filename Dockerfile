FROM golang:1.23-alpine AS builder
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN go build -o subpub-service ./cmd/server

FROM alpine:3.19
WORKDIR /app
COPY --from=builder /app/subpub-service .
COPY config.yaml .
EXPOSE 50051
CMD ["./subpub-service"]
