# Stage 1: Build
FROM golang:1.24-alpine AS builder

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY . .

RUN CGO_ENABLED=0 GOOS=linux go build -o /service-b-app

# Stage 2: Run
FROM alpine:latest

WORKDIR /root/

COPY --from=builder /service-b-app .

EXPOSE 8081

CMD ["./service-b-app"]
