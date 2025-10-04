# Build stage för Go-backend
FROM golang:1.23-alpine AS builder

# Installera build-verktyg för SQLite
RUN apk add --no-cache gcc musl-dev sqlite-dev

WORKDIR /app

# Kopiera Go modules från backend
COPY backend/go.mod backend/go.sum ./
RUN go mod download

# Kopiera HELA backend-katalogen
COPY backend/ ./

# Bygg Go-binären (main.go ligger nu direkt i /app)
RUN CGO_ENABLED=1 GOOS=linux go build -a -installsuffix cgo -o main .

# Runtime stage
FROM alpine:latest

# Installera ca-certificates för HTTPS och sqlite
RUN apk --no-cache add ca-certificates sqlite-libs

WORKDIR /root/

# Kopiera Go-binären från builder
COPY --from=builder /app/main .

# Kopiera frontend
COPY frontend/ ./frontend/

# Exponera port
EXPOSE 8080

# Skapa katalog för databas
RUN mkdir -p /data

# Kör applikationen
CMD ["./main"]