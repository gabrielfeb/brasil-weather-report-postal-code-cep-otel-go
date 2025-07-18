# Stage 1: Build
FROM golang:1.24-alpine AS builder

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY . .

RUN CGO_ENABLED=0 GOOS=linux go build -o /service-a-app

# Stage 2: Run
FROM alpine:latest

WORKDIR /root/

COPY --from=builder /service-a-app .

EXPOSE 8080

CMD ["./service-a-app"]
